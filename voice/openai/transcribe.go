package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"

	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/voice"
)

// transcriptWire is the response /audio/transcriptions returns. Every optional
// block is a pointer or a slice so an absent one stays distinguishable: a local
// server usually sends no usage at all.
type transcriptWire struct {
	Text     string  `json:"text"`
	Task     string  `json:"task"`
	Language string  `json:"language"`
	Duration float64 `json:"duration"`
	Segments []struct {
		Start   float64 `json:"start"`
		End     float64 `json:"end"`
		Text    string  `json:"text"`
		Speaker string  `json:"speaker"`
	} `json:"segments"`
	Words []struct {
		Word       string   `json:"word"`
		Start      float64  `json:"start"`
		End        float64  `json:"end"`
		Speaker    string   `json:"speaker"`
		Confidence *float64 `json:"confidence"`
	} `json:"words"`
	Usage *usageWire `json:"usage"`
}

// usageWire is the endpoint's own accounting, when it reports any. Token counts
// are optional because a model may report only seconds and cost.
type usageWire struct {
	Seconds      float64  `json:"seconds"`
	TotalTokens  int      `json:"total_tokens"`
	InputTokens  int      `json:"input_tokens"`
	OutputTokens int      `json:"output_tokens"`
	Cost         *float64 `json:"cost"`
}

// transcribeJSONWire is the base64 body a server that implements only the JSON
// shape accepts.
type transcribeJSONWire struct {
	Model                  string         `json:"model"`
	InputAudio             inputAudioWire `json:"input_audio"`
	Language               string         `json:"language,omitempty"`
	Temperature            *float64       `json:"temperature,omitempty"`
	ResponseFormat         string         `json:"response_format,omitempty"`
	TimestampGranularities []string       `json:"timestamp_granularities,omitempty"`
}

// inputAudioWire is the base64 audio and its codec, because a JSON body carries
// no filename to infer it from.
type inputAudioWire struct {
	Data   string `json:"data"`
	Format string `json:"format"`
}

// Transcribe sends one audio clip and returns its text. The default shape is
// multipart, which every OpenAI-compatible server accepts; the base64 JSON shape
// is opt-in for a server that implements only that one.
func (c *Client) Transcribe(ctx context.Context, req voice.TranscribeRequest) (*voice.Transcript, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	key, err := c.resolveKey(ctx)
	if err != nil {
		return nil, err
	}
	var body []byte
	contentType := "application/json"
	if c.transcribeJSON {
		body, err = jsonTranscribeBody(req)
		if err != nil {
			return nil, err
		}
	} else {
		body, contentType, err = transcribeForm(req)
		if err != nil {
			return nil, err
		}
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/audio/transcriptions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("voice: creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)
	c.setHeaders(httpReq, key)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, transportError(ctx, err)
	}
	defer resp.Body.Close()
	data, err := readBody(resp.Body, maxJSONBody)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp, data, key)
	}
	out, err := decodeTranscript(data, req.Model)
	if err != nil {
		return nil, err
	}
	out.GenerationID = resp.Header.Get("X-Generation-Id")
	return out, nil
}

// jsonTranscribeBody builds the base64 shape.
func jsonTranscribeBody(req voice.TranscribeRequest) ([]byte, error) {
	wire := transcribeJSONWire{
		Model: req.Model,
		InputAudio: inputAudioWire{
			Data:   base64.StdEncoding.EncodeToString(req.Audio),
			Format: string(req.Format),
		},
		Language:    req.Language,
		Temperature: req.Temperature,
	}
	if req.Verbose {
		wire.ResponseFormat = "verbose_json"
	}
	wire.TimestampGranularities = append(wire.TimestampGranularities, req.Timestamps...)
	body, err := json.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("voice: encoding request: %w", err)
	}
	return body, nil
}

// transcribeForm builds the multipart body. The file part carries both the
// extension and the media type, because the codec is inferred from the name and
// a wrong one is a 400 rather than a useful error.
func transcribeForm(req voice.TranscribeRequest) ([]byte, string, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="clip.%s"`, req.Format))
	header.Set("Content-Type", req.Format.MIME())
	part, err := writer.CreatePart(header)
	if err != nil {
		return nil, "", fmt.Errorf("voice: building request: %w", err)
	}
	if _, err := part.Write(req.Audio); err != nil {
		return nil, "", fmt.Errorf("voice: building request: %w", err)
	}
	fields := [][2]string{{"model", req.Model}}
	if req.Language != "" {
		fields = append(fields, [2]string{"language", req.Language})
	}
	if req.Temperature != nil {
		fields = append(fields, [2]string{"temperature", strconv.FormatFloat(*req.Temperature, 'f', -1, 64)})
	}
	if req.Verbose {
		fields = append(fields, [2]string{"response_format", "verbose_json"})
	}
	for _, granularity := range req.Timestamps {
		fields = append(fields, [2]string{"timestamp_granularities[]", granularity})
	}
	for _, field := range fields {
		if err := writer.WriteField(field[0], field[1]); err != nil {
			return nil, "", fmt.Errorf("voice: building request: %w", err)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("voice: building request: %w", err)
	}
	return buf.Bytes(), writer.FormDataContentType(), nil
}

// decodeTranscript maps the wire response to the contract. An empty text is a
// valid answer for silent audio.
func decodeTranscript(data []byte, model string) (*voice.Transcript, error) {
	var wire transcriptWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil, fmt.Errorf("voice: parsing transcript: %w", err)
	}
	out := &voice.Transcript{
		Text:     strings.TrimSpace(wire.Text),
		Language: wire.Language,
		Duration: wire.Duration,
		Model:    model,
	}
	if wire.Usage != nil {
		usage, err := usageOf(wire.Usage)
		if err != nil {
			return nil, err
		}
		out.Usage = usage
		if out.Duration == 0 {
			out.Duration = wire.Usage.Seconds
		}
	}
	for _, segment := range wire.Segments {
		out.Segments = append(out.Segments, voice.Segment{
			Start: segment.Start, End: segment.End, Text: segment.Text, Speaker: segment.Speaker,
		})
	}
	for _, word := range wire.Words {
		out.Words = append(out.Words, voice.Word{
			Word: word.Word, Start: word.Start, End: word.End,
			Speaker: word.Speaker, Confidence: word.Confidence,
		})
	}
	return out, nil
}

// usageOf maps the wire usage block to the shared type. A reported block makes
// the cost provider-reported even when it is zero, which is a real free call;
// an absent block leaves the zero usage with an empty CostSource, because a
// local engine that reports nothing is not a failed cost lookup.
func usageOf(wire *usageWire) (provider.Usage, error) {
	if wire.Seconds < 0 || wire.TotalTokens < 0 || wire.InputTokens < 0 || wire.OutputTokens < 0 || (wire.Cost != nil && *wire.Cost < 0) {
		return provider.Usage{}, fmt.Errorf("voice: response has negative usage")
	}
	usage := provider.Usage{
		PromptTokens:     wire.InputTokens,
		CompletionTokens: wire.OutputTokens,
		TotalTokens:      wire.TotalTokens,
		CostSource:       "provider",
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	if wire.Cost != nil {
		usage.CostUSD = *wire.Cost
	}
	return usage, nil
}
