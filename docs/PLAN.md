# Nova — Plan

This is the working plan for Nova, a reusable agentic-loop harness in Go. It is
inspired by [pi](https://github.com/earendil-works/pi) and by the agent loops
already living in `her-go`, `polaris`, and `ivy`.

Status: **planning.** Milestone 0 was prototyped to de-risk the design (see
[`SPIKE.md`](SPIKE.md)); the plan below is what we intend to build for real.

## 1. Vision

A Go program should be able to add a capable agent loop with one import:

```go
agt := nova.New(nova.Config{
    Provider: nova.OpenRouter(apiKey),
    Model:    "openai/gpt-4o-mini",
    Tools:    nova.CoreTools(),   // read, write, edit, list, glob, grep, bash
})
answer, err := agt.Prompt(ctx, "summarize this repo")
```

and extend it the way pi extensions feel: register a tool, a provider, a
command, or an event handler without forking the core.

### 1.1 North star

The end state is a **full coding harness on par with pi** — our own, but built
on the same principles: a small core, a rich core toolset, sessions and
compaction, a first-class terminal UI, and extensions that are trivial to add.
Everything in M0–M4 is a step toward a harness we would choose to use daily as a
coding agent, not just a library other programs embed. The library and the
standalone agent are the same thing viewed from two sides.

## 2. Goals

- **Minimal core, useful out of the box.** The loop, the registry, core tools,
  sessions, and compaction ship together.
- **Provider-agnostic.** A small `Provider` interface; OpenRouter first, then any
  OpenAI-compatible endpoint (Ollama, LM Studio, vLLM, LiteLLM).
- **Two tool-authoring paths.** Go registration for logic-heavy tools; YAML +
  Markdown manifests for descriptions, schemas, agent scoping, and trace
  formatting.
- **Hookable.** Every meaningful loop stage is a nil-safe hook, so domain
  behavior (research nudges, PII redaction, permission gates, traces) lives
  outside the core.
- **Embeddable and standalone.** The same package powers a library import and a
  runnable CLI/TUI.
- **Pi-class as a coding agent.** The standalone harness should eventually match
  pi as a daily coding agent: core tools, sessions, compaction, a real TUI, and
  easy extension.
- **Observable and cost-aware.** A typed event stream and accumulated usage/cost
  from day one.

## 3. Non-goals (v1)

- A web UI, hosted service, or multi-tenant control plane.
- Every provider wire protocol. OpenAI-compatible first; others via the provider
  interface.
- A visual/declarative workflow engine. Nova runs loops, not DAGs.
- Sub-agents as a first-class v1 feature (planned after the core is stable).
- Replacing each project's domain logic. Nova is the substrate, not the product.

## 4. Principles

1. **The loop is small; behavior is hooks.** Anything domain-specific is a hook
   or an extension, never core.
2. **The transcript is the wire.** Messages map 1:1 onto the OpenAI
   chat-completions format so the provider layer stays thin.
3. **Tools are data plus a function.** A definition the model can read, plus
   code that runs.
4. **Failures are results, not crashes.** Tool errors go back to the model; tool
   panics are recovered; provider errors abort one run, not the process.
5. **Add, don't modify.** New tools and providers are registered from the
   outside.
6. **Zero required dependencies for the core.** The loop, registry, and
   OpenAI-compatible provider use only the standard library. YAML and the TUI
   are opt-in packages.

## 5. Milestones

Each milestone lists deliverables and an exit criterion.

### M0 — Spine (prototype complete; see `SPIKE.md`)

Deliverables: `message`, `provider` + `openaicompat` + `openrouter`, `tool`
interface + registry, core `fs`/`bash` tools, `agent` loop with hooks and
events, minimal CLI.

Exit criterion: an OpenRouter prompt can call tools and produce a final answer;
unit tests cover the loop, registry, provider SSE parsing, and the fs tools.

### M1 — Tool authoring and permissions

Deliverables:
- YAML manifest loader: `tools/<name>/tool.yaml` with `name`, `agent`,
  `description`, `hot`, `category`, `parameters`, `trace` (her-go shape).
- A Go handler binding by name, plus pure-Go `tool.New` tools.
- Tool annotations surfaced to a permission hook (`ReadOnly`, `Destructive`,
  `OpenWorld`, `Parallel`).
- Context files (project instructions) loaded into the system prompt.
- A `use_tools`-style deferred-tool loader for large tool sets.

Exit criterion: adding a tool is creating a directory with a manifest and a
handler, or calling `Register`; no central list to edit.

### M2 — Sessions and compaction

Deliverables:
- JSONL session store with tree entries (`id`, `parentId`), active branch,
  resume, branch, and fork.
- `SessionManager` as the authority for model context (pi semantics).
- Summarize-and-splice compaction at a user-message boundary; tool-result
  pruning at request time.
- Context-window accounting and a compaction trigger.
- Session cost/token stats.

Exit criterion: a multi-turn session survives process restart, branches cleanly,
and compacts without losing tool-call/tool-result pairing.

### M3 — TUI

Deliverables (Bubble Tea v2):
- Streaming transcript with text, reasoning, tool calls, and tool results.
- Input editor, slash commands, model picker, thinking level.
- Session list/switch; compaction indicator; token/cost footer.
- Tool-call rendering per tool (diff view for edits, command/output for bash),
  and an expandable transcript.

Exit criterion: Nova can be used as a daily coding agent from the terminal —
the first point at which it is comparable to pi as a coding harness.

### M3.5 — Coding-harness parity

Deliverables: the remaining pi-class affordances that make it a *good* coding
agent rather than a demo — `grep`/`glob` search tools, patch/diff editing,
project context files (`AGENTS.md`/`CLAUDE.md`-style), prompt templates,
permission confirmation UI, and cost/usage dashboards.

Exit criterion: a feature checklist against pi passes for everyday coding work,
with our own architecture throughout.

### M4 — Extensions and interop

Deliverables:
- `extension` package: an in-process `Extension` interface plus a registry
  (`RegisterTool`, `RegisterProvider`, `RegisterCommand`, `On(event, handler)`).
- MCP client so external servers' tools appear in the registry.
- Skills and prompt templates loaded from disk.
- Structured output and image inputs.

Exit criterion: a third party can add tools/commands via a Go package or an MCP
server without modifying Nova.

### M5 — Integrations

Deliverables: wire ivy's "Ask Ivy" through Nova behind its `llm.Gate`; migrate
her-go's `agent_engine`; port polaris's research loop with nudges as hooks.

Exit criterion: at least two production projects run on Nova with no bespoke
loop code.

## 6. Cross-cutting concerns

- **Security.** Tool annotations + a permission hook; path containment options;
  no secrets in logs; provider credentials resolved at request time.
- **Cost.** Every provider call reports tokens and, when available, cost; a cap
  hook can refuse calls (this is where ivy's gate attaches).
- **Caching.** Keep the system prefix stable and provider-agnostic; leave
  provider-specific prompt-cache hints to the provider implementation.
- **Testing.** A scriptable fake provider (already in the M0 spike) for
  deterministic loop tests; golden tests for the TUI; a live smoke test behind a
  build tag.

## 7. Open questions

- Is the canonical transcript `message.Message` sufficient for Anthropic-style
  content blocks, or do we need a richer content-part model before M4?
- Do steering/follow-up message queues (pi's model) belong in v1, or after M2?
- Should the TUI be a separate module from the library to keep the core tiny?
- How much of polaris's "research" vocabulary (citations, cards, charts) should
  be generic tools versus app-level hooks?
- Do we want a Starlark/JS scripting host once the extension API stabilizes?

## 8. Decision log

See [`DESIGN.md`](DESIGN.md#decision-log) for the locked decisions and the
alternatives considered.
