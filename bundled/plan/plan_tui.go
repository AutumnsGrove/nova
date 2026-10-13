//go:build !notui

package plan

// Card is the TUI card summary for a submitted plan.
func Card(plan string) string { return fmtPlan(plan) }
