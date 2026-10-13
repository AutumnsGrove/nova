// Command status-segment shows a turn counter in the footer.
package main

import (
	"fmt"

	"github.com/AutumnsGrove/nova/ext/sdk"
)

func main() {
	sdk.Run(func(h *sdk.Host) error {
		turns := 0
		h.On("turn_end", func(*sdk.Hook) (*sdk.HookDecision, error) {
			turns++
			_ = h.UI.Status("turns", []sdk.Segment{
				{Text: fmt.Sprintf("turn %d", turns), Style: "accent"},
			})
			return nil, nil
		})
		return nil
	})
}
