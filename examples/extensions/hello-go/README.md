# hello-go

A **Go SDK** extension: a tool and a command, written with `ext/sdk`.

`main.go` registers a `hello` tool and a `hello` slash command. `extension.yaml`
builds it with `go build` and runs the result.

## Run it

```
nova ext check ./examples/extensions/hello-go
nova ext install ./examples/extensions/hello-go
```

Then use the `hello` tool or type `/hello` in the TUI.
