// Command ask-demo asks the user for a choice and a note.
package main

import (
	"strings"

	"github.com/AutumnsGrove/nova/ext/sdk"
)

func main() {
	sdk.Run(func(h *sdk.Host) error {
		h.Command("ask", "Ask for a topic and a note", func(c *sdk.Command) error {
			topics, err := c.UI.Select(c.Context(), "Pick a topic", []sdk.Choice{
				{ID: "bugs", Label: "Bugs"},
				{ID: "docs", Label: "Docs"},
				{ID: "ideas", Label: "Ideas"},
			}, false)
			if err != nil {
				return err
			}
			note, err := c.UI.Input(c.Context(), "Add a note", "optional")
			if err != nil {
				return err
			}
			c.Write("topic=" + strings.Join(topics, ",") + " note=" + note + "\n")
			return nil
		})
		return nil
	})
}
