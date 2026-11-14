package openai

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/AutumnsGrove/nova/voice"
)

// speechWire is the body /audio/speech accepts. ResponseFormat is always sent:
// the endpoint defaults to pcm, and a caller that asked for a file almost never
// wants a headerless stream. The routing pin is deliberately absent — `provider`
// is OpenRouter's object, and an OpenAI-compatible server has no such field.
type speechWire struct {
	Model          string   `json:"model"`
	Input          string   `json:"input"`
	Voice          string   `json:"voice"`
	ResponseFormat string   `json:"response_format"`
	Speed          *float64 `json:"speed,omitempty"`
	Instructions   string   `json:"instructions,omitempty"`
}

// errNoAudio reports a 200 with no bytes, which is not a clip.
var errNoAudio = errors.New("voice: response carried no audio")

// Speak synthesizes one clip. The response is raw audio, so the format and the
// pcm layout come from the Content-Type.
func (c *Client) Speak(ctx context.Context, req voice.SpeechRequest) (*voice.Clip, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	key, err := c.resolveKey(ctx)
	if err != nil {
		return nil, err
	}
	format := req.Format
	if format == "" {
		format = voice.FormatMP3
	}
	body, err := jsonBody(speechWire{
		Model: req.Model, Input: req.Input, Voice: req.Voice,
		ResponseFormat: string(format), Speed: req.Speed, Instructions: req.Instructions,
	})
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/audio/speech", body)
	if err != nil {
		return nil, fmt.Errorf("voice: creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	c.setHeaders(httpReq, key)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, transportError(ctx, err)
	}
	defer resp.Body.Close()
	data, err := readBody(resp.Body, voice.MaxClipBytes)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp, data, key)
	}
	if len(data) == 0 {
		return nil, errNoAudio
	}
	contentType := resp.Header.Get("Content-Type")
	clip := &voice.Clip{
		Bytes:        data,
		Format:       formatFromMediaType(contentType, format),
		MIME:         format.MIME(),
		Model:        req.Model,
		Voice:        req.Voice,
		GenerationID: resp.Header.Get("X-Generation-Id"),
	}
	if mt, _ := mediaType(contentType); mt != "" {
		clip.MIME = mt
	}
	switch clip.Format {
	case voice.FormatPCM:
		clip.SampleRate, clip.Channels, clip.BitsPerSample = pcmLayout(contentType)
	case voice.FormatWAV:
		// A WAV body is PCM with a header, and a chunked caller would otherwise
		// concatenate whole files into one unplayable stream. Report the samples
		// and the layout the fmt chunk gives, so Join and WAV stay correct.
		pcm, rate, channels, bits, ok := wavToPCM(data)
		if !ok {
			return nil, errBadWAV
		}
		clip.Bytes = pcm
		clip.Format = voice.FormatPCM
		clip.MIME = voice.FormatPCM.MIME()
		clip.SampleRate, clip.Channels, clip.BitsPerSample = rate, channels, bits
	}
	// No cost is read or estimated: a local engine bills nothing and reports
	// nothing, so Usage stays zero with an empty CostSource.
	return clip, nil
}
