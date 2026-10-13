// Command summarize summarizes text with a nested model call.
package main

import (
	"encoding/json"
	"strings"

	"github.com/AutumnsGrove/nova/ext/sdk"
	"github.com/AutumnsGrove/nova/message"
	"github.com/AutumnsGrove/nova/provider"
)

func main() {
	sdk.Run(func(h *sdk.Host) error {
		h.Command("summarize", "Summarize the given text", func(c *sdk.Command) error {
			text := strings.TrimSpace(c.Args)
			if text == "" {
				c.Write("usage: /summarize <text>\n")
				return nil
			}
			raw, err := h.Complete(c.Context(), provider.Request{
				Messages: []message.Message{{
					Role:    message.RoleUser,
					Content: "Summarize in one sentence:\n\n" + text,
				}},
			})
			if err != nil {
				return err
			}
			var out struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			}
			_ = json.Unmarshal(raw, &out)
			c.Write(strings.TrimSpace(out.Message.Content) + "\n")
			return nil
		})
		return nil
	})
}
