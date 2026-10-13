# path-guard

A **Go hook** that blocks `write` and `edit` calls whose path is absolute or
escapes with `..`. It shows the `tool_call` hook and a block decision.

## Run it

```
nova ext check ./examples/extensions/path-guard
nova ext install ./examples/extensions/path-guard
```

Then ask the model to write `/etc/passwd` and watch it get blocked.
