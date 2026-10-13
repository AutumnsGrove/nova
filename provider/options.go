package provider

// MergeOptions returns a new map. Keys in over replace keys in base, except
// when both values are map[string]any, which merge recursively. Slices are
// replaced rather than appended. Neither input is modified, and nil inputs are
// treated as empty.
func MergeOptions(base, over map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(over))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		if bm, ok := out[k].(map[string]any); ok {
			if om, ok := v.(map[string]any); ok {
				out[k] = MergeOptions(bm, om)
				continue
			}
		}
		out[k] = v
	}
	return out
}

// NormalizeFinish maps provider-specific finish reasons onto the small set the
// agent loop understands: stop, length, tool_calls, content_filter, or error.
// An empty or unknown reason becomes "".
func NormalizeFinish(s string) string {
	switch s {
	case "stop", "end_turn", "stop_sequence":
		return "stop"
	case "length", "max_tokens":
		return "length"
	case "tool_calls", "tool_use", "function_call":
		return "tool_calls"
	case "content_filter", "safety":
		return "content_filter"
	case "error":
		return "error"
	default:
		return ""
	}
}
