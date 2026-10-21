// Package sdk is the Go SDK for writing a Nova subprocess extension. It
// handles framing, request correlation, deadlines, and panics, so an extension
// only describes its tools, commands, and hooks.
//
// A minimal extension:
//
//	func main() {
//		sdk.Run(func(h *sdk.Host) error {
//			h.Tool(sdk.Tool{
//				Name:        "hello",
//				Description: "Greets someone.",
//				Params:      sdk.Object(sdk.String("name", "Who to greet", sdk.Required)),
//				ReadOnly:    true,
//			}, func(c *sdk.Call) (sdk.Result, error) {
//				return sdk.Text("Hello, %s!", c.String("name")), nil
//			})
//			return nil
//		})
//	}
//
// Handlers run on their own goroutines, so state they share needs a lock. The
// package imports nothing from Nova, so an extension links only itself.
package sdk
