// Package chat is the one-shot completion seam for non-agent work: titles,
// suggestions, extraction, and the other cheap calls an app makes outside
// the loop. It runs one provider call through the same routing, headers,
// and usage reporting a loop turn gets, without constructing an agent.
package chat

import (
	"cmp"
	"context"

	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/provider"
)

// Request is one completion. Fields mirror the provider.Request options a
// one-shot call needs; anything more belongs in the agent loop.
type Request struct {
	// Model is a catalog id, never a wire slug. It is translated to the
	// wire slug for the call while the catalog id travels as ModelID, so a
	// router sees the stable name — this is the Model/ModelID trap that
	// once sent subagents to the wrong backend. An id the catalog does not
	// know passes through as its own slug.
	Model    string
	Messages []message.Message
	Tools    []provider.ToolDef
	// ToolChoice and ParallelToolCalls are the dialects' tool controls;
	// see provider.Request. Nil sends nothing.
	ToolChoice        any
	ParallelToolCalls *bool
	MaxTokens         int
	Temperature       *float64
	ReasoningEffort   string
	ProviderOptions   map[string]any
	ExtraHeaders      map[string]string
	// PromptCache overrides the explicit cache marker; nil decides from
	// the catalog. See provider.Request.
	PromptCache *bool
}

// Complete runs one completion against p and returns the finalized response:
// content and tool calls on Message, accounting on Usage, attribution on
// Provider. Stream events are not delivered; pass p.Stream directly when
// they matter.
func Complete(ctx context.Context, p provider.Provider, req Request) (*provider.Response, error) {
	model := req.Model
	modelID := req.Model
	if m, ok := provider.GetModel(req.Model); ok {
		model = cmp.Or(m.Slug, req.Model)
	}
	return p.Stream(ctx, provider.Request{
		Model:             model,
		ModelID:           modelID,
		Messages:          req.Messages,
		Tools:             req.Tools,
		ToolChoice:        req.ToolChoice,
		ParallelToolCalls: req.ParallelToolCalls,
		MaxTokens:         req.MaxTokens,
		Temperature:       req.Temperature,
		ReasoningEffort:   req.ReasoningEffort,
		ProviderOptions:   req.ProviderOptions,
		ExtraHeaders:      req.ExtraHeaders,
		PromptCache:       req.PromptCache,
	}, nil)
}
