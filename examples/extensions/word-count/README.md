# word-count

A **declarative tool** with an argument: it runs `wc -w` on `{{.path}}`.

`extension.yaml` shows how a manifest turns a command into a tool without any
process. Nova substitutes `{{.path}}` and runs `wc` directly, never through a
shell.

## Run it

```
nova ext check ./examples/extensions/word-count
nova ext install ./examples/extensions/word-count
```

Then ask the model to count the words in a file.
