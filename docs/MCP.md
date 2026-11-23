# MCP servers

Nova speaks the [Model Context Protocol](https://modelcontextprotocol.io) to servers it starts
itself, and offers their tools to the model beside the built-in ones. A server is a program on
`$PATH`; Nova is the client, never the server.

This is v1 and it is deliberately small: **stdio transport, and tools only.** Resources, prompts,
sampling, elicitation, HTTP+SSE and OAuth are out of scope, and so is letting an extension declare
or start a server (the `mcp` manifest capability is reserved and warns — see
[`EXTENSIONS.md`](EXTENSIONS.md)).

## Config

```yaml
mcp:
  servers:
    - name: fs                          # becomes the tool-name prefix; required
      command: /usr/local/bin/mcp-fs     # required
      args: ["/tmp", "--read-only"]
      env:                               # added to the inherited environment
        LOG_LEVEL: warn
      cwd: ~/work                        # the child's working directory
      timeout_ms: 30000                  # one call, default 60000
```

- `name` must be lowercase letters, digits, `-` or `_`, and unique: it is the first half of every
  tool name the server contributes.
- A server with no name, no command, a name that is not legal or a duplicate is dropped at load with
  a warning naming it and its index. It is never silently ignored.
- `env` is added to the environment the host inherits. A server's environment is never logged or
  echoed in an error, and the credential variables listed in `extensions.scrub_env` are not passed
  to one.

## What a server contributes

Each tool becomes a registry tool named `<server>__<tool>`, described as `[<server>] <description>`.
The prefix makes a collision with a built-in tool impossible and shows the model where the tool came
from; if the name is somehow already taken, that is one warning naming both and the earlier
registration keeps it. The server's `inputSchema` is the tool's parameter schema unchanged, and its
annotation hints (`readOnlyHint`, `destructiveHint`, `openWorldHint`) become the `tool.Info`
annotations the permission policy already understands.

A `tools/call` result maps onto the tool contract:

| MCP content | Nova |
|---|---|
| `text` blocks | joined into `Result.Content` |
| `image` blocks | `Result.Parts`, shown as one synthetic message after the tool batch |
| anything else | an omission note appended to `Result.Content`, never a dropped result |
| `isError: true` | a failed result, which the model reads and can react to |

## Lifecycle

- Servers connect and list their tools when the server starts. Startup does not wait for them: a
  slow or hanging server cannot delay a run, and a server that fails to start or handshake contributes
  no tools and reports a warning instead.
- `get_extensions` lists one row per server with `kind: "mcp"`, its state, its tool count and its last
  error, so a dead server is visible rather than absent.
- `notifications/tools/list_changed` re-lists that server and makes the registry match, without a
  reload.
- `reload` re-reads the config and replaces every connection: a changed command, argument or
  environment arrives that way. The old processes are gone before the new ones start.
- Shutdown closes each server's stdin, waits two seconds, sends SIGTERM to its process group, waits
  two seconds, then SIGKILLs. No process survives it, including anything the server started.
- One call is bounded by `timeout_ms` and by the run's own cancellation. A message line over 16 MiB,
  malformed JSON, or a crash disconnects that server with a typed error rather than being retried.

## Permissions

An MCP tool is an ordinary tool: `permissions.rules` decide it by name (`fs__write`), and a `deny`
blocks the call before the server sees it. An MCP server is not a way around the policy, and it gets
no ambient authority the built-in tools do not have.

## Debugging

`nova doctor` starts each configured server, lists its tools and reports one line per server, so a
broken `command` or a bad argument is visible before it costs a run. `/mcp` in the TUI prints the
same table for a server that is already running.

Nova does not capture a server's stderr in an error message: it is another program's output, and it
may contain anything, including a credential. A server that will not start is run by hand — its
command and the config's `args` are all it takes — and usually says why there. Point `command` at
the server binary itself rather than at a shell wrapper that hides the reason.
