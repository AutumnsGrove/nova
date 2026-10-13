// Command clock-widget shows the current time in a text widget.
package main

import (
	"time"

	"github.com/AutumnsGrove/nova/ext/sdk"
)

func main() {
	sdk.Run(func(h *sdk.Host) error {
		h.On("turn_end", func(*sdk.Hook) (*sdk.HookDecision, error) {
			_ = h.UI.Widget("clock", "above_editor", map[string]any{
				"kind":  "text",
				"lines": []string{"clock " + time.Now().Format("15:04:05")},
			})
			return nil, nil
		})
		return nil
	})
}
