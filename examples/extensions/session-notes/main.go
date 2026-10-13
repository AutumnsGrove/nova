// Command session-notes keeps notes in session entries.
package main

import (
	"encoding/json"
	"strings"

	"github.com/AutumnsGrove/nova/ext/sdk"
)

// noteKind names the session entries this extension owns.
const noteKind = "session-notes.note"

func main() {
	sdk.Run(func(h *sdk.Host) error {
		h.EntryKind(noteKind)
		h.Command("note", "Append a note to the session", func(c *sdk.Command) error {
			text := strings.TrimSpace(c.Args)
			if text == "" {
				c.Write("usage: /note <text>\n")
				return nil
			}
			if err := h.AppendEntry(noteKind, map[string]string{"text": text}, true); err != nil {
				return err
			}
			c.Write("noted.\n")
			return nil
		})
		h.Command("notes", "List the notes kept in this session", func(c *sdk.Command) error {
			entries := h.Entries(noteKind)
			if len(entries) == 0 {
				c.Write("no notes.\n")
				return nil
			}
			for _, raw := range entries {
				var note struct {
					Text string `json:"text"`
				}
				_ = json.Unmarshal(raw, &note)
				c.Write("- " + note.Text + "\n")
			}
			return nil
		})
		return nil
	})
}
