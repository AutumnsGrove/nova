# Nova — Images

The contract for image support: what a content part is, where it is legal on the
wire, how it is normalized and bounded, how every front door carries it, how a
bespoke tool produces one, and how the TUI renders one inline. This document is
the reference for issue [#20](https://github.com/AutumnsGrove/nova/issues/20) and
for the Polaris integration ([#22](https://github.com/AutumnsGrove/nova/issues/22),
see [`INTEGRATION.md`](INTEGRATION.md)).

Status: **built.** The decisions here are recorded as D115 to D129 in
[`DECISIONS.md`](DECISIONS.md). Each slice changed this document in the same
commit that changed the contract.

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
- **Text first, then media.** Every dialect orders a text block before the image
  blocks, matching Polaris and OpenAI. Anthropic accepts either order; one order
  keeps the builders testable against a single golden.
- **Unknown types are rejected at the front door and dropped by a provider.** A
  `bad_request` names the offending `type` when a command carries one; a provider
  that meets an unknown type in resumed history drops that part and leaves the
  rest, so an old session stays usable after a Nova upgrade.
- **A `data:` URL is normalized to `Data`.** A remote `http(s)` URL passes through
  only on a dialect that accepts a URL; on a dialect that needs base64 (Anthropic)
  it is omitted with a text note. Nova never fetches a model-supplied URL.

### Media only on user messages

OpenAI chat-completions allows image parts only on `user` messages; a `tool`
message is text-only, and a provider rejects a tool batch that is not one
assistant call followed by its results. Nova therefore never emits media on an
assistant or tool message. A tool that has an image returns it in its result, and
the loop appends a synthetic user message after the *entire* tool batch — the
rule DeepSeek and OpenAI both depend on. Polaris does the same thing
(`agent/driver.go` flushes `ctx.PendingImageMessages` after every tool result,
never between them). The synthetic message is marked (`message.Message.Synthetic`),
carried in the session and the events, so a client renders the image in the tool
card (pi's placement) rather than as a user bubble, and a replayed transcript does
not look like the user typed it.

### Capability

`provider.Model` gains `InputModalities []string` (empty means text-only),
mirroring OpenRouter's `input_modalities` and Polaris's `Multimodal bool`. It is
reported by `list_models` and in `state_changed` so a client can warn before a
prompt is sent.

**The agent enforces capability, on every request, not only on new images.**
`agent.turnRequest` drops parts from the request messages when the model cannot
take images and substitutes a one-line text placeholder per dropped part; the
transcript and the session keep the parts, so the TUI still shows them. Stripping
at request time is what makes a resumed session safe: a conversation that already
holds images, continued on a text-only model, must not resend them. The provider
stays wire-thin and never consults the catalog.

- A new prompt carrying images for a text-only model is rejected by `mode` before
the run starts with `bad_request`, naming the model and the fix — a better error
than the placeholder the agent would otherwise produce.
- A tool or history image on a text-only model becomes
  `[model does not support images; the image was omitted]`, so the run continues.
  This matches pi's `read` note.
- An **unknown model id** (an embedder's catalog Nova does not have) is text-only
  by default. An embedder that serves image models registers them with
  `provider.RegisterModel(... InputModalities: []string{"text", "image"})`, or
  sets `agent.Config.InputModalities` for its one model. Polaris registers its
  catalog; that is a small adapter and the documented price of the safe default.
- Never silently drop an image, and never send bytes a model cannot accept.

## Normalization and bounds

Every image is normalized exactly once, on the way into the transcript, whoever
produced it. The rules follow pi's `processImage` / `resizeImage`:

- **Accepted decode types:** `image/png`, `image/jpeg`, `image/gif`,
  `image/webp`, and `image/tiff` (macOS screenshots). The stored part keeps
  PNG/JPEG/GIF/WebP; TIFF and any other decodable image are converted to PNG. The
  TUI transcodes a non-PNG to PNG only for Kitty display.
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
- **Codecs come from `golang.org/x/image`.** `webp` and `tiff` for decode, `draw`
  for the resize filter (the standard library has no scaler). This is a D46
  addition: an allow-list row in `scripts/importcheck` scoped to the media package,
  and a `DECISIONS.md` entry. Without it WebP (which Polaris accepts) and TIFF
  (macOS clipboard screenshots) would be refused and resize quality would fall back
  to nearest-neighbour.
- **EXIF orientation is applied.** A JPEG from a phone carries its rotation in
  EXIF and Go's `image/jpeg` ignores it, so a portrait photo would be described and
  rendered sideways. Read the orientation tag (0x0112) from the APP1 segment and
  rotate on decode. A small internal reader, not another dependency.
- **Decode is bounded before it allocates.** `image.DecodeConfig` runs first; an
  image whose dimensions exceed the pixel cap is refused without a full decode, so
  a decompression bomb cannot exhaust memory. The normalizer takes untrusted input
  and gets a fuzz test (`TESTING.md` section 5).

### Limits

v1, enforced before a message enters the transcript or a command is accepted:

- Longest edge 2000 px after resize (pi's default), never upscaled.
- One encoded image ≤ 4.5 MiB (pi's `DEFAULT_MAX_BYTES`).
- At most 4 images per message, and ≤ 8 MiB of encoded image bytes per message.
  The **per-message total** is the number that matters: the session line and the
  `proto`/`ext` line caps are 16 MiB (`proto/codec.go:23`, `ext/conn.go:15`), and
  JSON escaping plus surrounding fields need headroom.
- Decoded pixel cap 80 megapixels, checked via `image.DecodeConfig`.
- A prompt or tool result over a limit is refused with a specific message, never
  truncated silently.

These are constants in the media package, so a test asserts the exact boundary
and one comment explains the 16 MiB ceiling they exist to respect.

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
(`[image]`, counted as about 1000 tokens so compaction still triggers), not as
text; the summariser cannot describe pixels and must not try to tokenise base64.
At request time, `compaction.PruneToolOutput`'s window also drops media parts
older than the kept turns, replacing each with `[image pruned]`, so a long
conversation does not carry stale pixels into every request. The session keeps
them, so the transcript still renders.

**Fork copies bytes.** `session.Fork` (`session/list.go:102`) rewrites every
branch entry into a new file, so a fork of an image-heavy session duplicates the
base64. Bounded by the limits above and acceptable for v1; if it hurts, the `Ref`
sidecar is the escape hatch and fork can share it by hash.

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
- **Path is reserved, and refused.** Every transport, `serve` included, refuses
  an attachment that carries `Path` and says to send `Data`; the in-process TUI
  reads the file itself and sends bytes. A path would otherwise let a caller name
  a file on the server, which is not a capability a prompt should have.
- Validate count, mime type, and size at the command boundary, before a run
  starts, and fail `bad_request` with a specific message.
- `get_messages` returns messages with their parts, so a client can replay and
  re-render a conversation. The TUI uses the same events.
- **Steer and follow-up carry the same attachments.** The agent's two queues
  change from `[]string` to `[]message.Message` (`agent/queue.go`), so a queued
  "look at this" keeps its image instead of degrading to text. `queue_update`
  still reports counts.

**The upload endpoint is built.** Inline `Data` still works for a one-step
caller; `POST /v1/attachments` is the two-step form a browser or a remote client
uses. It takes the bytes as the request body (the `Content-Type` is the mime
type, `?name=` the display name), validates them through the server's store, and
answers `{id, name, mime_type, size_bytes}`. `prompt`/`steer`/`follow_up` accept
`Attachment{ID: ...}` as an alternative to `Data`, resolved to bytes
server-side. Exactly one of `id` or `data`/`path` is set. The store lives in
memory in the server and expires when it closes; `serve.MaxBody` is 32 MiB.

**Additive, version stays 1.** Parts are new optional fields on existing types, so
an old client ignores them. The extension `tool_result` frame gains a `parts`
array (the SDK's `Result.Parts`) without a protocol bump; a declarative `exec`
tool has no way to produce one, which is the intended limit. The session entry
format changes only inside `message.Message`, so no session migration.

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
    Inject   func(message.Message) // queue a message after this tool batch
    AddUsage func(provider.Usage)  // report a nested call's tokens and cost
}
```

- **`Result.Parts` is the common case.** After the loop records every tool result
  in a batch, it appends **one** synthetic user message carrying every image from
  the batch in call order, with the results' captions joined as its text. One
  message, not one per result: consecutive user messages are awkward for several
  providers, and a batch's images belong to the same model turn. Order is
  deterministic and the batch is never interleaved.
- **`Context.Inject` is the escape hatch**, for a tool that needs a custom role,
  multiple messages, or app-supplied text — Polaris's
  `ctx.AddPendingImageMessage` maps directly onto it.
- **Subprocess extensions carry parts too.** The `tool_result` frame and
  `ext/sdk`'s `Result` gain a `parts` field. A declarative `exec` tool produces
  text, which is the correct limit; a real image tool is protocol code.
- **Nested model calls report their usage and may name a model.** `Complete`
  takes a `provider.Request`, so a "describe this image" tool puts an image part
  in it; `AddUsage` folds the response usage into the run, so a vision describe is
  counted in the footer, `get_usage`, and the session cost like any other call.
  `mode/ext.go:102` changes with it: today it forces `req.Model` to the current
  model and discards the response usage. An empty `ModelID`/`Model` means "the
  current model"; a non-empty catalog `ModelID` is selected through the router, so
  a tool can ask for a fallback vision model (Polaris's `MultimodalModel`), or the
  embedder supplies its own `Complete`.
- **`read` on an image path** returns
  `Result{Content: "Read image file [image/png]", Parts: [...]}` and normalizes
  first. This is pi's `read` (`dist/core/tools/read.js`), and it makes
  `@screenshot.png` and "point the model at this file" work.

## Package layout

Three new leaves, each with its own budget in the nearest chunk spec and in
[`ARCHITECTURE.md`](ARCHITECTURE.md), and each with a layer rule in
`scripts/importcheck`:

- **`media`** — decode, resize, transcode, dimension parsing, EXIF orientation,
  and the limits. It imports `message` (a leaf) and returns `message.ContentPart`
  values. Imported by `agent` (the request-time strip and tool injection), `mode`
  (protocol attachments), and `tools/fs` (the image branch of `read`).
- **`tui/image`** — capability detection, Kitty/iTerm2 encoding, and cell-size
  math, so `tui/render`'s budget is untouched. Imports `media` for dimensions.
  `tui/chat` is its only consumer.
- **`clipboard`** — the OS clipboard seam and per-platform readers. A leaf with no
  Nova imports, used by `tui/ui`; the fake lives in `testkit`.

A `message.ContentPart` is the only shape that crosses all three.

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
- **Settings.** `display.show_images` (default true when the terminal is
  image-capable), `display.image_width_cells` (default 60; choices 60/80/120),
  `display.auto_resize_images` (default true), and `images.block_images` (default
  false, agent-enforced: it strips parts from every provider request while the
  session and TUI keep them). Persisted with `config.SetUser` like the existing
  display rows.

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
4. Show it when the terminal supports it (`display.show_images`, default true
   when `tui/image` detects a protocol, false otherwise), and verify on a real
   Kitty-family terminal before claiming support. A PTY smoke test under
   `testkit` covers detection and line accounting, not pixels.

The capability detector and encoders belong in a small leaf package (`tui/image`)
with its own line budget, consumed by `tui/chat`; the detector is a seam with a
fake so no test reads the real environment or draws a real image.

**Clipboard.** `ctrl+v` (pi's key) reads the OS clipboard image through the
`clipboard` leaf: macOS `osascript` asking for `«class PNGf»` (the pasteboard
converts on request), Wayland `wl-paste --type image/png`, X11
`xclip -selection clipboard -t image/png -o`, WSL PowerShell. Every reader runs
under a short timeout (2 s) so a wedged clipboard cannot hang the TUI, and a host
with no clipboard (SSH, container, headless) gets a clear notice, not silence. The
reader returns normalized bytes plus a name and mime type, never a path. The bytes
are written to `os.TempDir()/nova-clipboard-<pid>/<n>.<ext>` so the editor can show
a path and a Finder-style drag works, and the same bytes ride the `prompt`
attachment; the directory is removed on a clean exit and swept on the next start.

**Pending attachments.** A pasted or `@`-picked file is a chip in a one-row list
above the editor (`name ×`), removed with backspace or a click. `pendingSend.atts`
(`tui/ui/update.go`) already carries names for the user bubble; the protocol
attachment carries the bytes. The row is one line on top of the editor height from
#18.

**`@path` attaches on submit.** The editor already completes file paths
(`tui/ui/complete.go`). On submit, a whitespace-delimited token that resolves to a
file whose header decodes as an image is read into an attachment and the token in
the text is replaced with the file's base name; a path that is not an image is
left alone. This is the same end state as `ctrl+v`, reached by typing.

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

- **iTerm2 support timing.** Kitty first; iTerm2 once the Bubble Tea repaint
  interaction is proven. If the proof is bad, iTerm2 stays on the text fallback
  and the doc says so.
- **`serve` bandwidth.** A base64 image in a `get_messages` response and in an
  event is real traffic over Tailscale. Bounded by the limits; revisit only if a
  real conversation makes it hurt.
- **Fork growth.** `session.Fork` duplicates image bytes; acceptable under the
  limits, but the first session with a dozen images will say whether the `Ref`
  sidecar is needed sooner.
