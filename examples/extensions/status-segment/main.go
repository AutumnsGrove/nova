// Command status-segment shows a turn counter in the footer.
package main

import (
	"fmt"
	"sync/atomic"

	"github.com/AutumnsGrove/nova/ext/sdk"
)

func main() {
	sdk.Run(func(h *sdk.Host) error {
		// Hooks run on their own goroutines, so shared state is atomic.
		var turns atomic.Int32
		h.On("turn_end", func(*sdk.Hook) (*sdk.HookDecision, error) {
			_ = h.UI.Status("turns", []sdk.Segment{
				{Text: fmt.Sprintf("turn %d", turns.Add(1)), Style: "accent"},
			})
			return nil, nil
		})
		return nil
	})
}
