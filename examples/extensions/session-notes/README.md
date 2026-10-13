# session-notes

A **stateful Go command**: `/note <text>` appends a note to the session and
`/notes` lists them. The notes are kept in session entries, so `/reload` and
resume replay them.

## Run it

```
nova ext check ./examples/extensions/session-notes
nova ext install ./examples/extensions/session-notes
```

Then `/note buy milk`, `/reload`, and `/notes`.
