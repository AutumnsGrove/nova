// Command hello-go is the SDK example: the smallest useful Nova extension.
package main

import (
	"github.com/AutumnsGrove/nova/ext/sdk"
)

func main() {
	sdk.Run(func(h *sdk.Host) error {
		h.Tool(sdk.Tool{
			Name:        "hello",
			Description: "Greets someone by name.",
			Params:      sdk.Object(sdk.String("name", "Who to greet", sdk.Required)),
			ReadOnly:    true,
		}, func(c *sdk.Call) (sdk.Result, error) {
			return sdk.Text("Hello, %s!", c.String("name")), nil
		})
		return nil
	})
}
