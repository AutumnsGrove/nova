// Package ask is the bundled ask extension: a model-facing `ask` tool that
// presents a multi-question form and returns the answers as text.
package ask

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/AutumnsGrove/nova/ext"
	"github.com/AutumnsGrove/nova/proto"
	"github.com/AutumnsGrove/nova/tool"
)

// Question kinds and the form limits.
const (
	kindSingle = "single"
	kindMulti  = "multi"
	kindText   = "text"

	maxQuestions    = 8
	maxOptions      = 8
	maxPrompt       = 500
	maxLabel        = 100
	noUserMessage   = "No interactive user is available. Make a reasonable assumption, state it, and continue."
	dismissedAnswer = "The user dismissed the form without answering. Ask in plain text or proceed with a stated assumption."
)

// option, question, and input are the tool's argument shapes.
type option struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Preview     string `json:"preview"`
}

type question struct {
	ID         string   `json:"id"`
	Prompt     string   `json:"prompt"`
	Kind       string   `json:"kind"`
	Options    []option `json:"options"`
	AllowOther *bool    `json:"allow_other"`
}

type input struct {
	Questions []question `json:"questions"`
}

// Extension is the ask bundled extension.
type Extension struct {
	env *ext.Env
}

// New returns a fresh ask extension.
func New() *Extension { return &Extension{} }

// Name is the extension name.
func (e *Extension) Name() string { return "ask" }

// Init registers the ask tool.
func (e *Extension) Init(env *ext.Env) error {
	e.env = env
	return env.Tool(tool.New(tool.Info{
		Name: "ask",
		Description: "Ask the user one or more questions. Notes the user writes are authoritative; " +
			"a skipped question means they have no preference.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"questions": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"id":     map[string]any{"type": "string"},
							"prompt": map[string]any{"type": "string"},
							"kind":   map[string]any{"type": "string", "enum": []string{kindSingle, kindMulti, kindText}},
							"options": map[string]any{
								"type": "array",
								"items": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"id":          map[string]any{"type": "string"},
										"label":       map[string]any{"type": "string"},
										"description": map[string]any{"type": "string"},
										"preview":     map[string]any{"type": "string"},
									},
								},
							},
							"allow_other": map[string]any{"type": "boolean"},
						},
						"required": []string{"id", "prompt", "kind"},
					},
				},
			},
			"required": []string{"questions"},
		},
		ReadOnly: true,
		Parallel: false,
	}, e.run))
}

// run validates the questions, presents the form, and formats the answers.
func (e *Extension) run(ctx context.Context, args json.RawMessage, _ *tool.Context) (tool.Result, error) {
	var in input
	if err := json.Unmarshal(args, &in); err != nil {
		return tool.Errorf("invalid arguments: %v", err), nil
	}
	questions, err := validate(in.Questions)
	if err != nil {
		return tool.Errorf("%v", err), nil
	}
	if e.env == nil || e.env.UI == nil || !e.env.HasUI("form") {
		return tool.Errorf("%s", noUserMessage), nil
	}
	req := proto.UIRequest{
		Type:      proto.FrameUIRequest,
		Method:    "form",
		Title:     "Questions",
		Questions: toProto(questions),
		AllowSkip: true,
	}
	resp, err := e.env.UI.Request(ctx, req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return tool.Errorf("cancelled"), nil
		}
		return tool.Errorf("the form could not be shown: %v", err), nil
	}
	if resp.Cancelled {
		return tool.Text("%s", dismissedAnswer), nil
	}
	text := formatAnswers(questions, resp)
	details, _ := json.Marshal(map[string]any{"questions": questions, "answers": resp.Answers})
	return tool.Result{Content: text, Details: details}, nil
}

// validate checks the form limits and downgrades an under-specified choice
// question to free text.
func validate(in []question) ([]question, error) {
	if len(in) == 0 {
		return nil, fmt.Errorf("at least one question is required")
	}
	if len(in) > maxQuestions {
		return nil, fmt.Errorf("%d questions is over the limit of %d", len(in), maxQuestions)
	}
	seen := map[string]bool{}
	out := make([]question, 0, len(in))
	for _, q := range in {
		if strings.TrimSpace(q.ID) == "" {
			return nil, fmt.Errorf("every question needs a non-empty id")
		}
		if seen[q.ID] {
			return nil, fmt.Errorf("duplicate question id %q", q.ID)
		}
		seen[q.ID] = true
		if len([]rune(q.Prompt)) > maxPrompt {
			return nil, fmt.Errorf("question %q prompt is over %d characters", q.ID, maxPrompt)
		}
		switch q.Kind {
		case kindSingle, kindMulti, kindText:
		default:
			return nil, fmt.Errorf("question %q has unknown kind %q", q.ID, q.Kind)
		}
		if len(q.Options) > maxOptions {
			return nil, fmt.Errorf("question %q has %d options, over the limit of %d", q.ID, len(q.Options), maxOptions)
		}
		optSeen := map[string]bool{}
		for _, o := range q.Options {
			if strings.TrimSpace(o.ID) == "" {
				return nil, fmt.Errorf("question %q has an option with an empty id", q.ID)
			}
			if optSeen[o.ID] {
				return nil, fmt.Errorf("question %q has duplicate option id %q", q.ID, o.ID)
			}
			optSeen[o.ID] = true
			if len([]rune(o.Label)) > maxLabel {
				return nil, fmt.Errorf("question %q option %q label is over %d characters", q.ID, o.ID, maxLabel)
			}
		}
		if (q.Kind == kindSingle || q.Kind == kindMulti) && len(q.Options) < 2 {
			q.Kind = kindText
		}
		out = append(out, q)
	}
	return out, nil
}

// toProto converts validated questions to the wire shape.
func toProto(in []question) []proto.UIQuestion {
	out := make([]proto.UIQuestion, 0, len(in))
	for _, q := range in {
		pq := proto.UIQuestion{ID: q.ID, Prompt: q.Prompt, Kind: q.Kind, Notes: true, AllowOther: q.AllowOther}
		for _, o := range q.Options {
			pq.Options = append(pq.Options, proto.UIOption{ID: o.ID, Label: o.Label, Description: o.Description, Preview: o.Preview})
		}
		out = append(out, pq)
	}
	return out
}

// ToolNames lists the tool Init registers.
func (e *Extension) ToolNames() []string { return []string{"ask"} }
