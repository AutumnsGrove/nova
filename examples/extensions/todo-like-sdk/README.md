# todo-like-sdk

A **stateful** extension: a `todo` tool that keeps its list in session entries and
replays them on reload.

This is the subprocess port of the bundled `tasks` extension, used by the tasks
equivalence test (TSK29). It shows `session:append_entry` and replay.

## Run it

```
nova ext check ./examples/extensions/todo-like-sdk
nova ext install ./examples/extensions/todo-like-sdk
```

Then use the `todo` tool; the list survives `/reload` and resume.
