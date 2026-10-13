---
name: extension-author
description: Write, check, install, and reload a Nova extension. Use when the user asks to add a tool, command, hook, widget, or provider to Nova.
---

# Writing a Nova extension

Nova can extend itself. Work in this order, and read the docs before writing code.

## 1. Read the docs

- `nova docs extensions` — the extension guide: the two tiers, the manifest,
  declarative tools, command hooks, capabilities, install, and reload.
- `nova docs protocol` — the JSONL protocol frames a subprocess speaks.

## 2. Pick the nearest example

Run `nova docs examples` to list the examples, then read the closest one. Copy its
shape: a declarative tool, a command hook, a Go SDK tool, a status segment, a
widget, a form, session entries, or a `Complete` call.

## 3. Write the extension

- `extension.yaml` with `name`, `version`, `description`, `nova: ">=0.1"`, and only
  the capabilities you use.
- Prefer a **declarative tool** when the tool is a description plus a command.
  Arguments are substituted, never interpreted by a shell.
- Use the Go SDK for a subprocess extension: `h.Tool`, `h.Command`, `h.On`,
  `h.AppendEntry`, and `h.Complete`.

## 4. Check it

Run `nova ext check ./<dir>`. It validates the manifest and starts the process to
confirm it registers. Fix every problem before installing.

## 5. Install and reload

Run `nova ext install ./<dir>` for a local directory, then `/reload` in the TUI.
Exercise the tool or command and confirm the result.

## Rules

- Do nothing at startup except register. Start long-lived work from a tool,
  command, or `session_start`.
- Keep state in session entries with `append_entry`, so `/reload` and resume
  rebuild it. Never keep it only in memory.
- Truncate large tool output and say where the full output lives.
- Return errors as results (`is_error: true`), never crash the process.
- Never interpolate a value into a shell string.
