//go:build !notui

package subagent

import "fmt"

// Card is the one-line TUI summary for a sub-agent run: the turn count, the
// tools it used, and the cost when the provider reported one.
func Card(turns int, tools []string, costUSD float64) string {
	summary := fmt.Sprintf("subagent: %d turn(s), %d tool(s)", turns, len(tools))
	if costUSD > 0 {
		summary += fmt.Sprintf(", $%.4f", costUSD)
	}
	return summary
}
