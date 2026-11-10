package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/voice"
)

// speechWire is the body /audio/speech accepts. ResponseFormat is pinned to a
// documented value rather than left out: the endpoint defaults to pcm, and a
// caller that asked for a file almost never wants a headerless stream.
type speechWire struct {
	Model          string   `json:"model"`
	Input          string   `json:"input"`
	Voice          string   `json:"voice"`
	ResponseFormat string   `json:"response_format"`
	Speed          *float64 `json:"speed,omitempty"`
	Instructions   string   `json:"instructions,omitempty"`
}

// Speak synthesizes one clip. The response is raw audio, so the format and the
// pcm layout come from the Content-Type and the generation id from a header.
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
	body, err := json.Marshal(speechWire{
		Model: req.Model, Input: req.Input, Voice: req.Voice,
		ResponseFormat: string(format), Speed: req.Speed, Instructions: req.Instructions,
	})
	if err != nil {
		return nil, fmt.Errorf("voice: encoding request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/audio/speech", bytes.NewReader(body))
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
	clip := &voice.Clip{
		Bytes:        data,
		Format:       formatFromMediaType(resp.Header.Get("Content-Type"), format),
		MIME:         format.MIME(),
		Model:        req.Model,
		Voice:        req.Voice,
		GenerationID: resp.Header.Get("X-Generation-Id"),
	}
	if mt, _ := mediaType(resp.Header.Get("Content-Type")); mt != "" {
		clip.MIME = mt
	}
	if clip.Format == voice.FormatPCM {
		clip.SampleRate, clip.Channels, clip.BitsPerSample = pcmLayout(resp.Header.Get("Content-Type"))
	}
	if c.costLookup && clip.GenerationID != "" {
		if cost, ok := c.generationCost(ctx, clip.GenerationID); ok {
			clip.Usage = provider.Usage{CostUSD: cost, CostSource: "provider"}
		}
	}
	return clip, nil
}
