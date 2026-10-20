// Package responses implements the OpenAI Responses API with streaming and
// function calling. OpenCode Go serves its GPT and Grok models through it.
package responses

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/provider/ssestream"
)

// Defaults. A zero in Config means "use the default".
const (
	defaultMaxOutputTokens = 4096
)

// Config configures a Provider. BaseURL is the API root; "/responses" is
// appended.
type Config struct {
	Name            string
	BaseURL         string
	APIKey          string
	APIKeyFunc      func(ctx context.Context) (string, error)
	Headers         map[string]string
	HTTPClient      *http.Client
	ConnectTimeout  time.Duration
	IdleTimeout     time.Duration
	MaxLineBytes    int
	MaxOutputTokens int
}

// Provider is a Responses-API provider.
type Provider struct {
	cfg Config
}

// New builds a provider from cfg.
func New(cfg Config) *Provider {
	if cfg.Name == "" {
		cfg.Name = "responses"
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.MaxOutputTokens <= 0 {
		cfg.MaxOutputTokens = defaultMaxOutputTokens
	}
	return &Provider{cfg: cfg}
}

// Name returns the provider id.
func (p *Provider) Name() string { return p.cfg.Name }

// Stream performs one streaming Responses request.
func (p *Provider) Stream(ctx context.Context, req provider.Request, onEvent func(provider.StreamEvent)) (*provider.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if req.Model == "" {
		return nil, fmt.Errorf("responses: model is required")
	}
	if p.cfg.BaseURL == "" {
		return nil, fmt.Errorf("responses: base URL is required")
	}
	body, err := p.buildBody(req)
	if err != nil {
		return nil, err
	}
	key := p.cfg.APIKey
	if p.cfg.APIKeyFunc != nil {
		key, err = p.cfg.APIKeyFunc(ctx)
		if err != nil {
			return nil, &provider.Error{Message: "resolving API key", Retryable: false, Err: err}
		}
	}
	headers := http.Header{}
	for k, v := range p.cfg.Headers {
		headers.Set(k, v)
	}
	for k, v := range req.ExtraHeaders {
		headers.Set(k, v)
	}
	if key != "" {
		headers.Set("Authorization", "Bearer "+key)
	}
	url := p.cfg.BaseURL + "/responses"
	s, err := ssestream.Open(ctx, ssestream.Config{
		HTTPClient:     p.cfg.HTTPClient,
		ConnectTimeout: p.cfg.ConnectTimeout,
		IdleTimeout:    p.cfg.IdleTimeout,
		MaxLineBytes:   p.cfg.MaxLineBytes,
	}, url, headers, body)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	return p.consume(s, req, onEvent)
}

// buildBody renders the request and merges ProviderOptions at the top level.
func (p *Provider) buildBody(req provider.Request) ([]byte, error) {
	instructions, input := buildInput(req.Messages)
	if input == nil {
		input = []inputItem{}
	}
	r := request{
		Model:        req.Model,
		Instructions: instructions,
		Input:        input,
		Stream:       true,
		Tools:        flattenTools(req.Tools),
		ToolChoice:   req.ToolChoice,
		CacheControl: provider.RequestCacheControl(req),
	}
	if req.Temperature != nil {
		r.Temperature = req.Temperature
	}
	max := req.MaxTokens
	if max <= 0 {
		max = p.cfg.MaxOutputTokens
	}
	r.MaxOutputTokens = &max
	if req.ReasoningEffort != "" {
		r.Reasoning = &reasoning{Effort: req.ReasoningEffort}
	}
	data, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("responses: encode request: %w", err)
	}
	if len(req.ProviderOptions) == 0 {
		return data, nil
	}
	var base map[string]any
	if err := json.Unmarshal(data, &base); err != nil {
		return nil, fmt.Errorf("responses: encode request: %w", err)
	}
	merged := provider.MergeOptions(base, req.ProviderOptions)
	return json.Marshal(merged)
}

// callAcc accumulates one function call across its deltas.
type callAcc struct {
	callID string
	name   string
	args   strings.Builder
	index  int
}

// assembler groups streamed function-call fragments.
type assembler struct {
	calls map[string]*callAcc
	order []string
}

func newAssembler() *assembler { return &assembler{calls: map[string]*callAcc{}} }

// key is the call id, falling back to the output index.
func callKey(id string, index int) string {
	if id != "" {
		return id
	}
	return fmt.Sprintf("#%d", index)
}

func (a *assembler) start(id string, index int, callID, name string) {
	key := callKey(id, index)
	if _, ok := a.calls[key]; !ok {
		a.order = append(a.order, key)
	}
	a.calls[key] = &callAcc{callID: callID, name: name, index: index}
}

func (a *assembler) append(id string, index int, delta string) {
	key := callKey(id, index)
	acc := a.calls[key]
	if acc == nil {
		acc = &callAcc{index: index}
		a.calls[key] = acc
		a.order = append(a.order, key)
	}
	acc.args.WriteString(delta)
}

// assembled returns the calls in output-index order.
func (a *assembler) assembled() []message.ToolCall {
	keys := append([]string(nil), a.order...)
	sort.SliceStable(keys, func(i, j int) bool { return a.calls[keys[i]].index < a.calls[keys[j]].index })
	var out []message.ToolCall
	for _, key := range keys {
		acc := a.calls[key]
		if acc.name == "" {
			continue
		}
		id := acc.callID
		if id == "" {
			id = key
		}
		args := acc.args.String()
		if args == "" {
			args = "{}"
		}
		out = append(out, message.ToolCall{
			ID:       id,
			Type:     "function",
			Function: message.FunctionCall{Name: acc.name, Arguments: args},
		})
	}
	return out
}

// consume reads the event stream into a Response.
func (p *Provider) consume(s *ssestream.Stream, req provider.Request, onEvent func(provider.StreamEvent)) (*provider.Response, error) {
	result := &provider.Response{Model: req.Model}
	result.Message.Role = message.RoleAssistant
	var text, reasoning strings.Builder
	asm := newAssembler()
	finish := ""
	terminal := false

	partial := func() *provider.Response {
		result.Message.Content = text.String()
		result.Message.Reasoning = reasoning.String()
		return result
	}

	for {
		raw, err := s.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return partial(), err
		}
		ev, err := decodeEvent(raw.Data)
		if err != nil {
			continue
		}
		if ev.Type == "" {
			ev.Type = raw.Name
		}
		switch ev.Type {
		case "response.output_text.delta":
			text.WriteString(ev.Delta)
			emit(onEvent, provider.StreamEvent{Type: provider.StreamTextDelta, Text: ev.Delta})
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			reasoning.WriteString(ev.Delta)
			emit(onEvent, provider.StreamEvent{Type: provider.StreamReasoningDelta, Text: ev.Delta})
		case "response.output_item.added":
			if ev.Item.Type == "function_call" {
				asm.start(ev.Item.ID, ev.OutputIndex, ev.Item.CallID, ev.Item.Name)
			}
		case "response.function_call_arguments.delta":
			asm.append(ev.ItemID, ev.OutputIndex, ev.Delta)
		case "response.completed":
			terminal = true
			finish = "stop"
			u := ev.Response.Usage
			result.Usage = provider.Usage{
				PromptTokens:     u.InputTokens,
				CompletionTokens: u.OutputTokens,
				ReasoningTokens:  u.OutputDetails.ReasoningTokens,
				CachedTokens:     u.InputDetails.CachedTokens,
				TotalTokens:      u.TotalTokens,
			}
		case "response.incomplete":
			terminal = true
			if ev.Response.IncompleteDetails.Reason == "max_output_tokens" {
				finish = "length"
			} else {
				finish = "stop"
			}
		case "response.failed":
			msg := ev.Response.Error.Message
			if msg == "" {
				msg = ev.Error.Message
			}
			if msg == "" {
				msg = "response failed"
			}
			return partial(), &provider.Error{Message: msg, Retryable: false}
		case "error":
			msg := ev.Error.Message
			if msg == "" {
				msg = "responses error"
			}
			return partial(), &provider.Error{Message: msg, Retryable: false}
		}
		if terminal {
			break
		}
	}

	partial()
	calls := asm.assembled()
	if len(calls) > 0 {
		result.Message.ToolCalls = calls
		for i := range calls {
			emit(onEvent, provider.StreamEvent{Type: provider.StreamToolCall, ToolCall: &calls[i]})
		}
		if finish == "" || finish == "stop" {
			finish = "tool_calls"
		}
	}
	result.FinishReason = provider.NormalizeFinish(finish)
	if !terminal {
		return result, &provider.Error{Message: "stream ended before response.completed", Retryable: true}
	}
	return result, nil
}

// emit calls onEvent when it is set.
func emit(onEvent func(provider.StreamEvent), ev provider.StreamEvent) {
	if onEvent != nil {
		onEvent(ev)
	}
}
