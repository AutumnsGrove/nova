package provider

// CacheControl is the explicit prompt-cache marker the dialects share. Its
// wire form is {"type":"ephemeral"}. A top-level marker is automatic caching:
// the provider places the breakpoint at the end of the prompt and advances it
// as the loop appends, so every turn reuses the previous turn's prefix.
type CacheControl struct {
	Type string `json:"type"`
}

// RequestCacheControl returns the marker req should carry, or nil for none.
//
// An explicit Request.PromptCache always wins, so a caller can opt in on a
// model that caches by itself or opt out on one that does not. Otherwise the
// decision comes from the catalog: a known model that does not cache
// implicitly gets a marker, and a model the catalog does not know gets none,
// so an unknown endpoint never receives a parameter it may reject.
func RequestCacheControl(req Request) *CacheControl {
	var want bool
	if req.PromptCache != nil {
		want = *req.PromptCache
	} else {
		m, ok := lookupModel(req)
		want = ok && !m.ImplicitCaching
	}
	if !want {
		return nil
	}
	return &CacheControl{Type: "ephemeral"}
}

// lookupModel resolves the catalog entry for a request. The stable ModelID
// wins over the wire slug, because a slug alone does not name a provider.
func lookupModel(req Request) (Model, bool) {
	if req.ModelID != "" {
		if m, ok := GetModel(req.ModelID); ok {
			return m, true
		}
	}
	if req.Model != "" {
		return GetModel(req.Model)
	}
	return Model{}, false
}
