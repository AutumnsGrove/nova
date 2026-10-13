# hello

The smallest extension: a **declarative tool** that needs no process at all.

`extension.yaml` declares a `hello` tool whose `exec` prints a greeting. Nova runs
the command directly, with `{{.name}}` substituted, and never through a shell.

## Run it

```
nova ext check ./examples/extensions/hello
nova ext install ./examples/extensions/hello
```

Then ask the model to use the `hello` tool.
