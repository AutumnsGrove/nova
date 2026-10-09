package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/provider"
	"github.com/AutumnsGrove/nova/tool"
)

const defaultMaxTurns = 25

// Run executes the agent loop over history and returns the final result.
//
// history is the prior transcript (user/assistant/tool messages); the system
// prompt is prepended from cfg.System. Run blocks until the model answers, the
// turn budget is exhausted, the context is cancelled, or a provider call fails.
func Run(ctx context.Context, cfg Config, history []message.Message) (*Result, error) {
	if cfg.Provider == nil {
		return nil, errors.New("agent: Provider is required")
	}
	if cfg.Model == "" {
		return nil, errors.New("agent: Model is required")
	}
	maxTurns := cfg.MaxTurns
	if maxTurns <= 0 {
		maxTurns = defaultMaxTurns
	}
	mode := cfg.ToolExecution
	if mode == "" {
		mode = Parallel
	}

	emit := func(e Event) {
		if cfg.OnEvent != nil {
			cfg.OnEvent(e)
		}
	}

	messages := make([]message.Message, 0, len(history)+2)
	if cfg.System != "" {
		messages = append(messages, message.System(cfg.System))
	}
	messages = append(messages, history...)

	tctx := &tool.Context{WorkDir: cfg.WorkDir, Data: cfg.ToolData}

	var totalUsage provider.Usage
	var lastText string

	for turn := 0; turn < maxTurns; turn++ {
		if err := ctx.Err(); err != nil {
			return finish(&Result{Messages: messages, Answer: lastText, Turns: turn, Usage: totalUsage}, err)
		}
		emit(Event{Type: EventTurnStart, Turn: turn})

		reqMessages := messages
		if cfg.TransformContext != nil {
			reqMessages = cfg.TransformContext(messages)
		}

		var toolDefs []provider.ToolDef
		if cfg.Tools != nil {
			toolDefs = cfg.Tools.Defs()
		}

		var text strings.Builder
		resp, err := cfg.Provider.Stream(ctx, provider.Request{
			Model:       cfg.Model,
			Messages:    reqMessages,
			Tools:       toolDefs,
			Temperature: cfg.Temperature,
			MaxTokens:   cfg.MaxTokens,
		}, func(ev provider.StreamEvent) {
			switch ev.Type {
			case provider.StreamTextDelta:
				text.WriteString(ev.Text)
				emit(Event{Type: EventTextDelta, Turn: turn, Text: ev.Text})
				if cfg.OnTextDelta != nil {
					cfg.OnTextDelta(ev.Text)
				}
			case provider.StreamReasoningDelta:
				emit(Event{Type: EventReasoningDelta, Turn: turn, Text: ev.Text})
				if cfg.OnReasoningDelta != nil {
					cfg.OnReasoningDelta(ev.Text)
				}
			}
		})
		if err != nil {
			emit(Event{Type: EventError, Turn: turn, Err: err})
			return finish(&Result{Messages: messages, Answer: lastText, Turns: turn + 1, Usage: totalUsage}, err)
		}

		totalUsage.Add(resp.Usage)

		assistant := resp.Message
		assistant.Role = message.RoleAssistant
		if assistant.Content == "" && text.Len() > 0 {
			assistant.Content = text.String()
		}
		messages = append(messages, assistant)
		if assistant.Content != "" {
			lastText = assistant.Content
		}

		turnUsage := resp.Usage
		emit(Event{Type: EventTurnEnd, Turn: turn, Text: assistant.Content, Usage: &turnUsage})

		if len(assistant.ToolCalls) == 0 {
			result := &Result{Messages: messages, Answer: assistant.Content, Turns: turn + 1, Usage: totalUsage}
			emit(Event{Type: EventDone, Turn: turn})
			return result, nil
		}

		results := executeTools(ctx, cfg, mode, assistant.ToolCalls, tctx)
		for i, call := range assistant.ToolCalls {
			content := results[i].Content
			if content == "" {
				content = "(empty result)"
			}
			messages = append(messages, message.ToolResult(call.ID, content))
		}

		if cfg.FinishTurn != nil {
			decision := cfg.FinishTurn(Turn{Index: turn, Message: assistant, Results: results})
			if decision.End {
				result := &Result{Messages: messages, Answer: lastText, Turns: turn + 1, Usage: totalUsage}
				emit(Event{Type: EventDone, Turn: turn})
				return result, nil
			}
		}
	}

	result := &Result{
		Messages:  messages,
		Answer:    lastText,
		Turns:     maxTurns,
		Usage:     totalUsage,
		Exhausted: true,
	}
	emit(Event{Type: EventDone, Turn: maxTurns})
	return result, nil
}

func finish(result *Result, err error) (*Result, error) {
	return result, err
}

// executeTools dispatches the tool calls from one assistant message, preserving
// call order in the returned results regardless of completion order.
func executeTools(ctx context.Context, cfg Config, mode ToolExecutionMode, calls []message.ToolCall, tctx *tool.Context) []tool.Result {
	results := make([]tool.Result, len(calls))

	if mode == Sequential {
		for i, call := range calls {
			results[i] = runTool(ctx, cfg, call, tctx)
		}
		return results
	}

	var wg sync.WaitGroup
	for i, call := range calls {
		wg.Add(1)
		go func(i int, call message.ToolCall) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					res := tool.Errorf("internal error running %s: %v", call.Function.Name, r)
					results[i] = res
					emitToolResult(cfg, call, res)
				}
			}()
			results[i] = runTool(ctx, cfg, call, tctx)
		}(i, call)
	}
	wg.Wait()
	return results
}

func runTool(ctx context.Context, cfg Config, call message.ToolCall, tctx *tool.Context) tool.Result {
	hookCall := ToolCall{ID: call.ID, Name: call.Function.Name, Arguments: call.Function.Arguments}

	if cfg.BeforeToolCall != nil {
		if block := cfg.BeforeToolCall(hookCall); block != nil {
			res := tool.Errorf("blocked: %s", block.Reason)
			if cfg.AfterToolCall != nil {
				res = cfg.AfterToolCall(hookCall, res)
			}
			emitToolResult(cfg, call, res)
			return res
		}
	}

	var res tool.Result
	switch {
	case call.Function.Name == "":
		res = tool.Errorf("model requested a tool call with no name")
	case cfg.Tools == nil:
		res = tool.Errorf("no tools are registered")
	default:
		res = cfg.Tools.Execute(ctx, call.Function.Name, json.RawMessage(call.Function.Arguments), tctx)
	}

	if cfg.AfterToolCall != nil {
		res = cfg.AfterToolCall(hookCall, res)
	}
	emitToolResult(cfg, call, res)
	return res
}

func emitToolResult(cfg Config, call message.ToolCall, res tool.Result) {
	if cfg.OnEvent == nil {
		return
	}
	cfg.OnEvent(Event{
		Type:       EventToolResult,
		ToolCallID: call.ID,
		ToolName:   call.Function.Name,
		Arguments:  call.Function.Arguments,
		Result:     res.Content,
		IsError:    res.IsError,
	})
}
