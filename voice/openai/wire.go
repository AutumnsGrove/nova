package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/voice"
)

const (
	// maxJSONBody bounds a JSON response so a broken or hostile server cannot
	// exhaust memory.
	maxJSONBody = 8 << 20
	// maxErrorBodyKept and maxErrorMessage bound what a failed response may put
	// in an error that reaches logs and the screen.
	maxErrorBodyKept = 8 << 10
	maxErrorMessage  = 500
	// defaultPCMSampleRate, defaultPCMChannels and defaultPCMBits are the layout
	// assumed when the response does not say. Local engines overwhelmingly
	// answer 24 kHz mono 16-bit.
	defaultPCMSampleRate = 24000
	defaultPCMChannels   = 1
	defaultPCMBits       = 16
)

// readBody reads a bounded response body.
func readBody(r io.Reader, limit int) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, int64(limit)+1))
	if err != nil {
		return nil, fmt.Errorf("voice: reading response: %w", err)
	}
	if len(data) > limit {
		return nil, fmt.Errorf("voice: response is over the %d byte limit", limit)
	}
	return data, nil
}

// errorBody is the provider's failure shape. The same two spellings cover
// OpenAI, most gateways, and the local servers built on the same schema.
type errorBody struct {
	Error struct {
		Message string `json:"message"`
		Code    any    `json:"code"`
	} `json:"error"`
	Message string `json:"message"`
	Detail  string `json:"detail"`
}

// statusError maps a non-200 response to the provider's typed error. The API key
// is scrubbed from the message and the body, because a gateway may echo a
// rejected key back and neither reaches the screen or a log.
func statusError(resp *http.Response, body []byte, key string) *provider.Error {
	retryable, after := provider.Classify(resp.StatusCode, resp.Header.Get("Retry-After"))
	message := ""
	var e errorBody
	if json.Unmarshal(body, &e) == nil {
		message = e.Error.Message
		if message == "" {
			message = e.Message
		}
		if message == "" {
			message = e.Detail
		}
	}
	if message == "" {
		message = strings.TrimSpace(string(body))
	}
	if message == "" {
		message = http.StatusText(resp.StatusCode)
	}
	return &provider.Error{
		Status:     resp.StatusCode,
		Message:    truncateUTF8(scrub(message, key), maxErrorMessage),
		Body:       truncateUTF8(scrub(string(body), key), maxErrorBodyKept),
		Retryable:  retryable,
		RetryAfter: after,
	}
}

// scrub removes a credential from a string. A short key would match ordinary
// words, so it is left alone.
func scrub(s, secret string) string {
	if len(secret) < 8 {
		return s
	}
	return strings.ReplaceAll(s, secret, "[redacted]")
}

// truncateUTF8 cuts s to at most n bytes without splitting a multi-byte rune.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// transportError keeps a cancellation a cancellation.
func transportError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return fmt.Errorf("voice: request failed: %w", err)
}

// mediaType splits a Content-Type into its media type and parameters.
func mediaType(header string) (string, map[string]string) {
	mediaType, params, err := mime.ParseMediaType(header)
	if err != nil {
		return strings.TrimSpace(strings.Split(header, ";")[0]), nil
	}
	return mediaType, params
}

// formatFromMediaType maps a response media type to a format, falling back to
// the format the request asked for.
func formatFromMediaType(header string, fallback voice.Format) voice.Format {
	switch mediaType, _ := mediaType(header); mediaType {
	case "audio/mpeg", "audio/mp3":
		return voice.FormatMP3
	case "audio/pcm", "audio/l16":
		return voice.FormatPCM
	case "audio/wav", "audio/x-wav":
		return voice.FormatWAV
	}
	return fallback
}

// pcmLayout reads the sample layout from a pcm Content-Type such as
// "audio/pcm;rate=16000;channels=2", falling back to the documented defaults.
// The bit depth is rarely reported and 16 is the only depth that produces a
// plausible duration for a known-length response.
func pcmLayout(header string) (rate, channels, bits int) {
	_, params := mediaType(header)
	rate, channels, bits = defaultPCMSampleRate, defaultPCMChannels, defaultPCMBits
	if v, err := strconv.Atoi(params["rate"]); err == nil && v > 0 {
		rate = v
	}
	if v, err := strconv.Atoi(params["channels"]); err == nil && v > 0 {
		channels = v
	}
	if v, err := strconv.Atoi(params["bits"]); err == nil && v > 0 {
		bits = v
	}
	return rate, channels, bits
}

// jsonBody encodes a request body.
func jsonBody(v any) (*bytes.Reader, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("voice: encoding request: %w", err)
	}
	return bytes.NewReader(data), nil
}
