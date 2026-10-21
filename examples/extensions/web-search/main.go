// Command web-search is a provider-agnostic search tool: main registers the
// tool, backend.go holds the one interface a provider implements, and the
// README has the recipe for a real one.
package main

import (
	"context"

	"github.com/AutumnsGrove/nova/ext/sdk"
)

func main() {
	backend := Backend(canned{})
	sdk.Run(func(h *sdk.Host) error {
		h.Tool(sdk.Tool{
			Name:        "web_search",
			Description: "Search the web and return titles, URLs, and snippets. Fetch a result with web_fetch.",
			Params: sdk.Object(
				sdk.String("query", "What to search for", sdk.Required),
				sdk.Int("limit", "How many results (default 5, max 10)"),
			),
			ReadOnly:  true,
			OpenWorld: true,
			Parallel:  true,
		}, func(c *sdk.Call) (sdk.Result, error) {
			text, err := search(context.Background(), backend, c.String("query"), c.Int("limit"))
			if err != nil {
				return sdk.Text("error: %v", err), nil
			}
			return sdk.Text("%s", text), nil
		})
		return nil
	})
}
