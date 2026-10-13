// Command todo-like-sdk is a subprocess port of the bundled tasks extension,
// built on the Go SDK. It is the equivalence fixture for C18: the same call
// script must give the same results and the same session snapshots as the
// in-process extension.
package main

import (
	"strings"

	"github.com/AutumnsGrove/nova/bundled/tasks"
	"github.com/AutumnsGrove/nova/ext/sdk"
)

func main() {
	sdk.Run(func(h *sdk.Host) error {
		state := tasks.NewState()

		h.Tool(sdk.Tool{
			Name: "todo",
			Description: "Manage a task list. Every call returns the whole list. " +
				"More than one task may be doing; keep one in progress at a time.",
			Params: sdk.Object(
				sdk.String("action", "create|update|delete|list|clear", sdk.Required),
				sdk.String("id", "the task id"),
				sdk.String("title", "the task title"),
				sdk.String("status", "todo|doing|done|blocked"),
				sdk.String("detail", "a longer note"),
			),
			ReadOnly: false,
		}, func(c *sdk.Call) (sdk.Result, error) {
			next, text, changed, err := tasks.Apply(state, c.Raw())
			if err != nil {
				return sdk.Error("error: %v", err), nil
			}
			if changed {
				state = next
				if err := h.AppendEntry(tasks.StateKind(), state, true); err != nil {
					return sdk.Result{}, err
				}
			}
			return sdk.Text("%s", text), nil
		})

		h.Command("todos", "Show or clear the task list", func(c *sdk.Command) error {
			if strings.TrimSpace(c.Args) == "clear" {
				state.Tasks = nil
				if err := h.AppendEntry(tasks.StateKind(), state, true); err != nil {
					return err
				}
				c.Write("Tasks cleared.")
				return nil
			}
			_, text, _, err := tasks.Apply(state, []byte(`{"action":"list"}`))
			if err != nil {
				return err
			}
			c.Write(text)
			return nil
		})
		return nil
	})
}
