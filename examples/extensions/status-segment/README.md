# status-segment

A **Go hook** that updates a footer segment on every turn. It shows `turn_end`
and the `ui.status` capability.

## Run it

```
nova ext check ./examples/extensions/status-segment
nova ext install ./examples/extensions/status-segment
```

The footer shows `turn N` after each turn.
