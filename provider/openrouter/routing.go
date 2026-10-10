package openrouter

// Routing is a typed form of OpenRouter's provider-routing preferences. Every
// field is optional; the zero value asks OpenRouter to choose. Options renders
// it into the "provider" object that Request.ProviderOptions merges into the
// wire body, so callers never spell the field names themselves.
//
// Field names and meanings follow OpenRouter's provider-routing documentation.
type Routing struct {
	Order             []string
	Only              []string
	Ignore            []string
	AllowFallbacks    *bool
	Sort              string
	Quantizations     []string
	RequireParameters *bool
	DataCollection    string
	ZDR               *bool
	MaxPrice          *Price
	// PreferredMinThroughput is a floor on tokens per second, when the caller
	// wants speed over price.
	PreferredMinThroughput *float64
	// PreferredMaxLatency is a ceiling on seconds to first token.
	PreferredMaxLatency *float64
}

// Price is an OpenRouter price ceiling in US dollars per million tokens.
type Price struct {
	Prompt     float64
	Completion float64
}

// priceOption is the wire shape of Price inside max_price.
type priceOption struct {
	Prompt     float64 `json:"prompt"`
	Completion float64 `json:"completion"`
}

// Options renders the routing into Request.ProviderOptions. It returns nil
// when nothing is set, so an empty Routing adds nothing to the request.
func (r Routing) Options() map[string]any {
	provider := map[string]any{}
	if len(r.Order) > 0 {
		provider["order"] = r.Order
	}
	if len(r.Only) > 0 {
		provider["only"] = r.Only
	}
	if len(r.Ignore) > 0 {
		provider["ignore"] = r.Ignore
	}
	if r.AllowFallbacks != nil {
		provider["allow_fallbacks"] = *r.AllowFallbacks
	}
	if r.Sort != "" {
		provider["sort"] = r.Sort
	}
	if len(r.Quantizations) > 0 {
		provider["quantizations"] = r.Quantizations
	}
	if r.RequireParameters != nil {
		provider["require_parameters"] = *r.RequireParameters
	}
	if r.DataCollection != "" {
		provider["data_collection"] = r.DataCollection
	}
	if r.ZDR != nil {
		provider["zdr"] = *r.ZDR
	}
	if r.MaxPrice != nil {
		provider["max_price"] = priceOption{Prompt: r.MaxPrice.Prompt, Completion: r.MaxPrice.Completion}
	}
	if r.PreferredMinThroughput != nil {
		provider["preferred_min_throughput"] = *r.PreferredMinThroughput
	}
	if r.PreferredMaxLatency != nil {
		provider["preferred_max_latency"] = *r.PreferredMaxLatency
	}
	if len(provider) == 0 {
		return nil
	}
	return map[string]any{"provider": provider}
}
