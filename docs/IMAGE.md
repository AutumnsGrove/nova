# Nova — Images

The contract for image support: what a content part is, where it is legal on the
wire, how it is normalized and bounded, how every front door carries it, how a
bespoke tool produces one, and how the TUI renders one inline. This document is
the reference for issue [#20](https://github.com/AutumnsGrove/nova/issues/20) and
for the Polaris integration ([#22](https://github.com/AutumnsGrove/nova/issues/22),
see [`INTEGRATION.md`](INTEGRATION.md)).

Status: **designed, not built.** The decisions here are recorded as D115 in
[`DECISIONS.md`](DECISIONS.md). Implementation lands in slices; each slice
changes this document in the same commit when it changes the contract.

## Goals

- A screenshot can be attached to a prompt from the TUI, `print`, `json`, `rpc`,
  or `serve`, and reaches the model as an image.
- A tool can show the model an image — `read` on an image path, a `view_image`
  tool, a screenshot tool — without knowing anything about the wire format.
- A model that cannot take images is told so, and the image is omitted with a
  clear note rather than sent into the void.
- The model's own image output is shown inline in the TUI, the way pi does it.
- Polaris's existing image pipeline (`image_search`, `view_image` describe/see,
  uploaded workspace files) ports to Nova tools without re-architecting.

Out of scope: image *generation*, OCR, image editing, video, and inline rendering
of images the model did not receive.

## The contract

### A content part

The transcript stays OpenAI-shaped. Text stays `Content string`; images ride in a
new, additive `Parts` field. A message with no parts serializes byte-for-byte as
it does today, so every existing session, event, and golden test is unchanged.

```go
// message/
type ContentPart struct {
    Type     string `json:"type"`                 // "image"; "document" is the next kind
    MimeType string `json:"mime_type,omitempty"`  // "image/png", "image/jpeg", ...
    Data     string `json:"data,omitempty"`       // base64, no data: prefix
    URL      string `json:"url,omitempty"`        // optional; prefer Data
    Name     string `json:"name,omitempty"`       // display only, never sent
}

type Message struct {
    // ...existing fields...
    Content string        `json:"content,omitempty"`
    Parts   []ContentPart `json:"parts,omitempty"` // media only; never text
}
```

Rules:

- **`Content` is always the text.** `Parts` carries media only. A provider builds
  a string body when `len(Parts) == 0` and an array body otherwise; it prepends
  `Content` as the first text block when both are present. This is exactly what
  Polaris's `ChatMessage{Content, ImageURLs}` does (`llm/client.go` `MarshalJSON`).
- **Prefer `Data` over `URL`.** A data URL is provider-infrastructure-independent;
  Polaris emits `data:<mime>;base64,<bytes>` for the same reason. `URL` is for a
  caller that already holds a remote image and does not want to fetch it; the
  provider passes it through, and the absence of SSRF protection is the caller's
  problem, not the harness's.
- **No `Content any` and no parallel attachment type.** `tool.Details` is for the
  renderer only and is never sent to the model; images there are invisible to the
  model. Keeping one `ContentPart` used by `message`, the providers, `tool`,
  `proto`, and the extensions stops five shapes from drifting apart.

### Media only on user messages

OpenAI chat-completions allows image parts only on `user` messages; a `tool`
message is text-only, and a provider rejects a tool batch that is not one
assistant call followed by its results. Nova therefore never emits media on an
assistant or tool message. A tool that has an image returns it in its result, and
the loop appends a synthetic user message after the *entire* tool batch — the
rule DeepSeek and OpenAI both depend on. Polaris does the same thing
(`agent/driver.go` flushes `ctx.PendingImageMessages` after every tool result,
never between them).

### Capability

`provider.Model` gains `InputModalities []string` (empty means text-only),
mirroring OpenRouter's `input_modalities` and Polaris's `Multimodal bool`. It is
reported by `list_models` and in `state_changed` so a client can warn before a
prompt is sent.

- A prompt carrying images for a text-only model fails with `bad_request` before
  the run starts, with a message that names the model and the fix.
- A *tool* image on a text-only model is replaced with the note
  `[model does not support images; the image was omitted]`, so the run continues.
  This matches pi's `read` (`[Current model does not support images. The image
  will be omitted from this request.]`).
- Never silently drop an image, and never send bytes a model cannot accept.

## Normalization and bounds

Every image is normalized exactly once, on the way into the transcript, whoever
produced it. The rules follow pi's `processImage` / `resizeImage`:

- **Accepted types:** `image/png`, `image/jpeg`, `image/gif`, `image/webp`.
  Anything else that decodes as an image is converted to PNG.
- **Resize:** longest edge capped at 2000 px (pi's default), aspect preserved,
  never upscaled. A caller may lower the cap per model.
- **Encoded size:** under 4.5 MiB, pi's `DEFAULT_MAX_BYTES`. If a resize still
  does not fit, refuse with a clear message rather than send an over-limit image.
- **Hints come back as text.** A conversion or resize appends a note like
  `[Image: original 4000x3000, displayed at 2000x1500. Multiply coordinates by 2.00 to map to the original image.]`
  so the model can map coordinates. pi does this (`formatDimensionNote`), and it
  is the only way a model reasons about a downscaled screenshot.
- **Tool-produced images are normalized too.** A bespoke tool can hand back
  anything; it must not be able to overflow the provider request or the session.
  pi's `normalizeToolResultImages` keeps the original on a normalization failure
  rather than deleting the tool's output; Nova does the same, with a text note.
- **WebP decode needs a dependency.** `golang.org/x/image/webp` (and
  `golang.org/x/image/draw` if it does the resize) is a D46 stop-and-ask item: an
  allow-list row in `scripts/importcheck` and a `DECISIONS.md` entry. The
  alternative is refusing WebP, which would diverge from Polaris. Recommendation:
  add it, scoped to the image package.

### Where the bytes live

**Bounded inline base64 in the session.** There is no sidecar file and no second
storage path: a normalized image is small enough (≤4.5 MiB, ~6 MiB base64) to sit
inside Nova's 16 MiB JSONL line limit, and the whole session stays one
self-describing file that `session.tree`, fork, and compaction already understand.
The encoder caps the **total** encoded bytes per message, not just per image, so a
multi-image message cannot cross the line limit. If full-resolution history is
ever needed, add a `Ref` field to `ContentPart` and write sidecars then — additive
on disk, no wire change.

Compaction and the token estimate treat a part as a fixed placeholder
(`[image]`), not as text; the summariser cannot describe pixels and must not try
to tokenise base64.

## Front doors

`proto.Attachment` is the single wire shape for prompt media. It grows a mime type
and bytes and is read by `prompt` (and by `steer`/`follow_up` for parity), which
`mode` converts into a `message.ContentPart`:

```go
// proto/
type Attachment struct {
    Kind     string `json:"kind,omitempty"`      // "image" | "file" | "text"
    Name     string `json:"name,omitempty"`
    MimeType string `json:"mime_type,omitempty"`
    Data     string `json:"data,omitempty"`      // base64; the portable form
    Path     string `json:"path,omitempty"`      // local transports only
    Text     string `json:"text,omitempty"`
}
```

- **Bytes are the portable form.** `print`, `json`, `rpc`, and a remote `serve`
  client send `Data`. A remote client must never be able to name a server path,
  so `serve` ignores `Path`.
- **Path is a local convenience.** The in-process TUI may send a `Path` for a file
  the user just pasted; the server reads it before the run. This is the only
  place `Path` is honoured, and only when the transport is not remote.
- Validate count, mime type, and size at the command boundary, before a run
  starts, and fail `bad_request` with a specific message.
- `get_messages` returns messages with their parts, so a client can replay and
  re-render a conversation. The TUI uses the same events.

## The tool API

A tool that produces an image should not think about the wire. Two seams:

```go
// tool/
type Result struct {
    Content string
    // ...existing fields...
    Parts   []message.ContentPart // images the model should see
    Caption string                // text for the synthetic user message; optional
}

type Context struct {
    // ...existing fields...
    Inject func(message.Message) // queue a message after this tool batch
}
```

- **`Result.Parts` is the common case.** After the loop records every tool result
  in a batch, it appends one synthetic user message per result that has parts,
  with `Caption` (or a neutral caption) as the text. Order is deterministic and
  the batch is never interleaved.
- **`Context.Inject` is the escape hatch**, for a tool that needs a custom role,
  multiple messages, or app-supplied text — Polaris's
  `ctx.AddPendingImageMessage` maps directly onto it.
- **Subprocess extensions carry parts too.** The `tool_result` frame and
  `ext/sdk`'s `Result` gain a `parts` field. A declarative `exec` tool produces
  text, which is the correct limit; a real image tool is protocol code.
- **Nested vision calls work already.** `Context.Complete` takes a
  `provider.Request`, so a "describe this image" tool builds a request with one
  image part and gets text back. A fallback vision model is the app's choice: the
  app supplies `Complete` (as `mode` does today) and can route it to a different
  model.
- **`read` on an image path** returns
  `Result{Content: "Read image file [image/png]", Parts: [...]}` and normalizes
  first. This is pi's `read` (`dist/core/tools/read.js`), and it makes
  `@screenshot.png` and "point the model at this file" work.

## Inline rendering in the TUI

pi renders an image the model received inside the **tool card**, behind terminal
capability detection. Nova does the same. The pi implementation is the reference:

- **Capability detection is conservative and environment-based**
  (`pi-tui/dist/terminal-image.js`, `detectCapabilitiesFromEnvironment`). `tmux`
  and `screen` → no images (protocols are unreliable through them). Kitty,
  Ghostty, WezTerm, and Warp → the Kitty graphics protocol. iTerm2 → the iTerm2
  inline-image protocol. Windows Terminal, Alacritty, VS Code, Zed, JetBrains,
  and a plain Windows console → the text fallback. `PI_IMAGE_PROTOCOL` /
  `PI_TRUE_COLOR` override detection; Nova uses `NOVA_IMAGE_PROTOCOL` and
  `NO_COLOR` for the same purpose.
- **Ghostty is the reference terminal for the first slice.** It sets
  `TERM_PROGRAM=ghostty`, detects as Kitty, and implements the graphics
  protocol directly. Two cases fall back to text until overridden: under `tmux`
  or `screen`, where passthrough is unreliable, and on a remote host over SSH,
  where `TERM_PROGRAM` is not forwarded even though the escapes would render on
  the client — set `NOVA_IMAGE_PROTOCOL=kitty` there.
- **Kitty encoding** (`encodeKitty`): `\x1b_G` with
  `a=T,f=100,q=2,c=<cols>,r=<rows>,i=<id>,C=1`, base64 chunked at 4096
  characters with `m=1`/`m=0` continuation. `C=1` means the sequence does not
  move the cursor, which is what lets a line-oriented renderer reserve the image
  block itself. Kitty accepts PNG only, so a non-PNG is transcoded first; if the
  transcode fails the card shows the fallback text instead.
- **iTerm2 encoding** (`encodeITerm2`):
  `\x1b]1337;File=inline=1;size=<bytes>;width=<cols>;height=auto:<base64>\x07`.
- **Size is computed in cells, aspect preserved**
  (`calculateImageCellSize`): default cell size 9×18 px, `maxWidthCells` 60 by
  default (pi's setting offers 60/80/120), `maxHeightCells` derived from the
  width. Kitty enables an aspect-ratio optimisation because a cell is not square.
- **Line accounting is explicit.** The `Image` component returns the number of
  rows the image occupies: Kitty puts the sequence on the first line and pads the
  rest with blank lines; iTerm2 pads first, then moves the cursor back up and
  draws, so the renderer's cursor accounting stays inside the scroll area. Nova's
  transcript item must likewise return `rows` lines for the image block.
- **Fallback text** (`imageFallback`):
  `[Image: ~/path/shot.png image/png 1280x800]`, path shortened and, where OSC 8
  hyperlinks are supported, linked to `file://`. This is what `NO_COLOR` and a
  non-graphics terminal get.
- **Settings.** pi offers "show images" and "image width cells" (only when the
  terminal supports images) and an always-available "auto-resize images". Nova's
  `/settings` dialog (D114) is where `display.show_images` and
  `display.image_width_cells` go.

### The Nova risk to settle first

Nova's TUI is Bubble Tea, whose standard renderer diffs and repaints lines. Inline
graphics do not live in the text grid, so a naive implementation re-emits the
image escape on every repaint (flicker, duplicate placements) and scrolled-away
images can ghost. pi did not have this problem because `pi-tui` is its own
renderer with image metadata, placement-only commands, and crop-on-scroll
(`terminal-image.js`). The safe first version:

1. Render images **only in tool cards**, never in a user message or a diff, and
   only when `caps.images` is non-nil.
2. Prefer **Kitty** (`C=1`, reserved blank lines). Treat iTerm2 as a follow-up
   until the repaint interaction has a test.
3. Cache by `(data hash, mime, widthCells)` so a re-render is a no-op, and keep
   the image **out of the text export** (pi's `getRenderedTextOutput(result,
   showImages)`).
4. Ship it **off by default** behind `display.show_images`, and verify on a real
   Kitty-family terminal and a real iTerm2 before claiming support. A PTY smoke
   test under `testkit` covers detection and line accounting, not pixels.

The capability detector and encoders belong in a small leaf package (`tui/image`)
with its own line budget, consumed by `tui/chat`; the detector is a seam with a
fake so no test reads the real environment or draws a real image. The clipboard
reader (`ctrl+v` → OS clipboard image bytes) is a separate leaf package with a
fake, per-platform exec (macOS `osascript` PNGf, Linux `wl-paste`/`xclip`, WSL
PowerShell), and a clear notice where none is available.

## Polaris mapping

| Polaris | Nova |
|---|---|
| `llm.ChatMessage.ImageURLs []string` (data URLs) | `message.Message.Parts`; a data URL becomes `{Type:"image", MimeType, Data}` |
| `llm/client.go` `MarshalJSON` array content | the provider builders, same text-first ordering |
| `ctx.AddPendingImageMessage` | `tool.Context.Inject` or `tool.Result.Parts` |
| `agent/driver.go` flush after the tool batch | the loop's synthetic user message, same ordering |
| `view_image` "see" | `Result{Parts: ...}` on a tool with the image bytes |
| `view_image` "describe" | `Context.Complete` with one image part, app-routed to the vision model |
| `config.ModelConfig.Multimodal` | `provider.Model.InputModalities` |
| `fetchImageBytes` + `maxViewImageBytes` | the shared normalizer's limits |

The rest of the Polaris adoption (the loop, hooks, tool gating, events) is in
[`INTEGRATION.md`](INTEGRATION.md). Its image compatibility item is this document.

## Open questions

- **WebP dependency.** Add `golang.org/x/image`, or refuse WebP. Recommendation:
  add it (see above).
- **iTerm2 support timing.** Kitty first; iTerm2 once the Bubble Tea repaint
  interaction is proven. If the proof is bad, iTerm2 stays on the text fallback
  and the doc says so.
- **`serve` bandwidth.** A base64 image in a `get_messages` response and in an
  event is real traffic over Tailscale. Bounded by the caps; revisit only if a
  real conversation makes it hurt.
