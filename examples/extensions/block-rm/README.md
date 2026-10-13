# block-rm

A **command hook** that refuses destructive `rm -rf` calls.

`extension.yaml` registers a `tool_call` hook for the `bash` tool and runs
`check.sh`. The script reads the event JSON on stdin and prints
`{"block":true,"reason":"..."}` when the command contains `rm -rf`.

## Run it

```
nova ext check ./examples/extensions/block-rm
nova ext install ./examples/extensions/block-rm
```

Then ask the model to run `rm -rf` and watch it get blocked.
