# Nova — Voice

The contract for the two audio modalities: text to speech and speech to text.
What a request and a response are, how a voice is named, how the two directions
reach every front door, and what the caller still owns. This document is the
reference for issue [#85](https://github.com/AutumnsGrove/nova/issues/85); the
follow-up for local engines is [#86](https://github.com/AutumnsGrove/nova/issues/86).

Status: **proposed.** Nothing here is built yet. Each slice changes this
document in the same commit that changes the contract, the way
[`IMAGE.md`](IMAGE.md) did.

## Why

Nova already exposes chat (text and image in), decisions, and embeddings. Audio
is the remaining modality that both real consumers of Nova hand-roll:

- `polaris/voice` (872 lines) carries an OpenRouter STT client, an OpenRouter TTS
  client, a sentence chunker, a markdown stripper, a WAV wrapper, a hardcoded
  Kokoro voice list, and a hardcoded per-million-character cost table.
- `her-go/voice` (1194 lines) carries the same STT shape twice (JSON base64 and
  multipart), three TTS engines (OpenRouter, Piper, ElevenLabs), a fallback
  engine, and its own WAV/Opus conversion.

Both are doing the same work: one JSON request, one byte stream back, a model and
a voice that belong together, and a cost that the wire does not always report.
That is a primitive, not an application feature, so it belongs in Nova once.

OpenRouter now serves both directions on dedicated endpoints
(`/api/v1/audio/speech` and `/api/v1/audio/transcriptions`), with a live roster
filtered by output modality and per-model `supported_voices`. That is the v1
target.

## Goals

- An app synthesizes speech and transcribes audio with one import, without a
  vendor client, a retry loop, or a hand-maintained voice or price table.
- A role hides the vendor: an app asks for `narrate` or `default`, and changing
  the model or the voice is a config edit.
- Text long enough to be worth chunking, raw PCM that needs a header, and a
  long answer that has to be synthesized in pieces and joined back together
  have one correct implementation in the library.
- Every front door reaches both directions: the Go library, `tool.Context`,
  `print`, `json`, `rpc`, `serve`, a subprocess extension, and `nova speak` /
  `nova transcribe`.
- A failure is a typed result, and a local validation error never costs a
  request.

Out of scope: any TUI entry point (no playback, no microphone, no slash
command — a terminal is not an audio device), streaming synthesis, multi-speaker
`input` arrays, voice cloning (`input_references`), audio as a chat *input*
(a message content part, which is a multimodal-input feature and not
transcription), persisting audio in the session, bundled tools that speak or
listen, and any engine other than OpenRouter.

## The contract

One package, `voice`, with both directions. They share the format list, the size
caps, the model catalog and the role table, and one client owns both endpoints,
so splitting them into `tts` and `stt` would duplicate that seam and nothing
else. `media` is images and `voice` is speech, which is also how both consumers
already name it. This mirrors `embed`: a small contract, one wire adapter, and a
role decorator over the shared `roles` resolver.

```go
// voice/
type Speaker interface {
    Speak(ctx context.Context, req SpeechRequest) (*Clip, error)
}

type Transcriber interface {
    Transcribe(ctx context.Context, req TranscribeRequest) (*Transcript, error)
}

// Catalog reads a live roster for one direction.
type Catalog interface {
    List(ctx context.Context, kind Kind) ([]provider.Model, error)
}
```

`Kind` is `KindSpeech` (`"speech"`) or `KindTranscription`
(`"transcription"`): the same string OpenRouter's `output_modalities` filter
takes, so the roster and the catalog agree by construction.

### Speech

```go
type SpeechRequest struct {
    Model        string   `json:"model"`                  // catalog id or role name
    Input        string   `json:"input"`
    Voice        string   `json:"voice,omitempty"`        // required unless a role names one
    Format       Format   `json:"format,omitempty"`       // "mp3" or "pcm"; "" means mp3
    Speed        *float64 `json:"speed,omitempty"`        // a pointer, so 1.0 is explicit
    Instructions string   `json:"instructions,omitempty"` // tone and pacing, where supported
}

type Clip struct {
    Bytes         []byte         `json:"audio"` // base64 on the wire, raw in Go
    Format        Format         `json:"format"`
    MIME          string         `json:"mime"`
    SampleRate    int            `json:"sample_rate,omitempty"`     // pcm only
    Channels      int            `json:"channels,omitempty"`        // pcm only
    BitsPerSample int            `json:"bits_per_sample,omitempty"` // pcm only
    Model         string         `json:"model"`   // the configured id, not the provider's echo
    Voice         string         `json:"voice"`
    GenerationID  string         `json:"generation_id,omitempty"`
    Usage         provider.Usage `json:"usage"`
}
```

The speech endpoint returns a raw byte stream with no usage block. `Clip.Usage`
therefore carries `CostUSD` only when the generation lookup below answers;
`CostSource` is `"provider"` when it does and empty when it does not. An app that
needs exact spend can read `GenerationID` and resolve it itself.

The PCM layout comes from the response `Content-Type`, which OpenRouter sends as
`audio/pcm;rate=24000;channels=1`. The adapter parses `rate` and `channels` when
present and falls back to 24000 Hz, mono, 16-bit otherwise. Both consumers
currently hardcode Kokoro's numbers; this is where that stops.

`Input` is one string. OpenRouter also accepts an array of speaker turns for
multi-speaker models; v1 does not model it, and a caller that needs it is a
follow-up.

### Transcription

```go
type TranscribeRequest struct {
    Model       string   `json:"model"`
    Audio       []byte   `json:"audio"`  // raw bytes, base64 on the wire
    Format      Format   `json:"format"` // wav, mp3, flac, m4a, ogg, webm, aac
    Language    string   `json:"language,omitempty"`    // ISO-639-1; "" auto-detects
    Temperature *float64 `json:"temperature,omitempty"` // 0..1
    Timestamps  []string `json:"timestamps,omitempty"`  // "segment", "word"; verbose_json only
}

type Transcript struct {
    Text         string         `json:"text"`
    Language     string         `json:"language,omitempty"`
    Duration     float64        `json:"duration,omitempty"` // seconds
    Segments     []Segment      `json:"segments,omitempty"`
    Words        []Word         `json:"words,omitempty"`
    Model        string         `json:"model"`
    GenerationID string         `json:"generation_id,omitempty"`
    Usage        provider.Usage `json:"usage"`
}

type Segment struct {
    Start   float64 `json:"start"`
    End     float64 `json:"end"`
    Text    string  `json:"text"`
    Speaker string  `json:"speaker,omitempty"` // diarization, when the provider gives it
}

type Word struct {
    Word       string   `json:"word"`
    Start      float64  `json:"start"`
    End        float64  `json:"end"`
    Speaker    string   `json:"speaker,omitempty"`
    Confidence *float64 `json:"confidence,omitempty"`
}
```

An empty transcript is not an error. Silence and a two-second clip are real
answers, and Polaris turning that into an error is a UI decision, not a wire one;
Nova returns `Transcript{Text: ""}` and lets the caller decide what to say. The
adapter trims the wire's padding, so a whitespace-only answer reads as empty.

`Transcript.Usage` maps the endpoint's own accounting. Only `seconds` and
`cost` are guaranteed: `cost` becomes `CostUSD` with `CostSource: "provider"`,
and `usage.seconds` is not a token count, so it is `Transcript.Duration` rather
than a `provider.Usage` field. Token counts are mapped when present
(`input_tokens`/`output_tokens`), but `openai/whisper-1` returns none, so a
caller must not read a zero `PromptTokens` as "no tokens were billed".
`Transcript.Language` is whatever the provider reports, which may be a full name
(`"english"`) even though the request takes an ISO-639-1 code.

### Validation and caps

Validation is local, so a malformed request never costs a request:

| Rule | Value |
|---|---|
| `SpeechRequest.Input` | non-empty, at most `MaxSpeechRunes` (32,000) runes |
| `SpeechRequest.Voice` | required when the role does not name one |
| `SpeechRequest.Format` | `mp3` or `pcm` |
| `SpeechRequest.Speed` | 0.25 to 4.0, the widest range any documented model accepts |
| `TranscribeRequest.Audio` | non-empty, at most `MaxAudioBytes` (25 MiB) |
| `TranscribeRequest.Format` | `wav`, `mp3`, `flac`, `m4a`, `ogg`, `webm`, `aac` |
| `TranscribeRequest.Timestamps` | `segment` or `word`; refused without `verbose_json` |
| A response clip | at most `MaxClipBytes` (40 MiB) |

`MaxSpeechRunes` is a request-size guard, not a model limit: several models cap
`input` far lower (Seed Audio at 3,000 characters), which is exactly why
`voice.Chunks` exists and why `nova speak` chunks by default. A caller that sends
one huge string gets the provider's 400, not a silent truncation.

### Text preparation and joining

Both consumers hand-roll these, so the contract carries them:

```go
// Chunks splits text into sentence-sized pieces of at most maxRunes runes.
// A sentence longer than the cap is split at the last space that fits, and a
// word longer than the cap is hard-split. It never splits a rune and never
// returns an empty chunk. Blank input returns nil.
func Chunks(text string, maxRunes int) []string

// ChunkRunes is the default chunk size: long enough to cover a couple of short
// sentences, short enough that the first chunk synthesizes quickly.
const ChunkRunes = 300

// Join concatenates clips from one voice into one clip. Every clip must share
// the format, and a PCM clip must share the sample layout; otherwise Join
// refuses rather than producing a file that plays at the wrong speed. Usage is
// summed, the first clip's model and voice are kept, and GenerationID is
// dropped because one joined clip has many generations.
func Join(clips ...Clip) (Clip, error)

// WAV prepends a 44-byte RIFF/PCM header to a raw PCM clip, using the clip's
// own sample layout. It returns nil for a non-PCM clip or an unknown layout.
func WAV(c Clip) []byte
```

MP3 frames are self-contained, so a join is a byte concatenation; PCM is too,
with one WAV header written at the end. Kokoro's MP3 begins with a frame sync
(`ff f3`) and no ID3 tag, so a join of its clips is clean; an adapter that ever
sees an ID3 tag per clip must strip it rather than leave one mid-stream, and a
clip that does not round-trip is a test, not a surprise. Joining is what makes a
long answer possible without a model that accepts a long input.

Markdown stripping is deliberately not here. What to strip depends on the app's
own answer format (Polaris's citation links, Nova's tool cards), and a library
that guesses will read something wrong aloud. It is a ten-line regexp in the
caller.

## Roles

A voice belongs to a model: `alloy` is an OpenAI voice and returns 400 on
Voxtral or Kokoro. A speech role therefore bundles the model *and* the voice,
and a fallback has to restate both, because a fallback in another model cannot
inherit the primary's voice. This is the audio analogue of `embed`'s dimension
check: the thing that cannot survive a fallback must be restated by it.

```go
type VoiceRole struct {
    Model        string
    Voice        string
    Format       Format
    Speed        *float64
    Instructions string
    Timeout      time.Duration
    Fallback     *VoiceAttempt // optional; model and voice are its own
}

type VoiceAttempt struct {
    Model  string
    Voice  string
    Format Format
    Speed  *float64
}

type TranscribeRole struct {
    Model    string
    Fallback string
    Timeout  time.Duration
}
```

`Roles` implements both `Speaker` and `Transcriber`. The shared `roles.Table`
still owns the chain, the per-attempt timeout, the `-latest`/`~` alias refusal,
the unknown-role error and the usage accumulation; the decorator maps each model
in the chain to its own voice. A request whose `Model` contains a slash is a
direct catalog id and passes through, exactly as `embed` and `decide` do. A
direct speech id must carry its own `Voice`.

One attempt per model, then the fallback: a retryable 502 on the primary goes
straight to the fallback rather than repeating a call that just failed, which is
what Polaris already does and what the shared resolver's one-attempt-per-model
shape expresses.

```yaml
voice:
  allow_aliases: false   # -latest / ~ ids are refused by default
  cost_lookup: true      # best-effort /generation lookup, Nova's own front doors
  speech:
    roles:
      narrate:
        model: openai/gpt-4o-mini-tts-2025-12-15
        voice: alloy
        format: mp3
        speed: 1.0
        timeout_ms: 120000
        fallback:
          model: mistralai/voxtral-mini-tts-2603
          voice: en_paul_neutral
          format: mp3
  transcribe:
    roles:
      default:
        model: openai/whisper-1
        fallback: mistralai/voxtral-small-transcribe-3-0
        timeout_ms: 60000
```

The transcription timeout default is 60 s because OpenRouter's own upstream
timeout is 60 s per request and its docs say to split longer recordings; a
longer role timeout would only wait for a call the provider has already given up
on. `cost_lookup` is global rather than per role because it is a property of the
provider, not of a voice.

## The OpenRouter adapter

`voice/openrouter` is the preset: base URL, attribution headers, the key
resolved per request (so `/login` takes effect mid-run), the two endpoints, and
the live roster. A later `voice/openai` and the local engines
([#86](https://github.com/AutumnsGrove/nova/issues/86)) plug into the same
contract.

| Direction | Wire |
|---|---|
| Speak | `POST {base}/audio/speech` with `{model, input, voice, response_format, speed?, instructions?}`; raw bytes back |
| Transcribe | `POST {base}/audio/transcriptions` as `multipart/form-data` with `file` (named `clip.<format>`), `model`, `language`, `temperature`, `response_format`, `timestamp_granularities[]` |
| Roster | `GET {base}/models?output_modalities=speech` or `=transcription` |

Three wire decisions worth recording, and what the live API actually did on
2026-11-09 (a 1-second Kokoro clip, one call each):

| Check | Result |
|---|---|
| `audio/speech` `pcm` | `Content-Type: audio/pcm;rate=24000;channels=1`, `X-Generation-Id: gen-tts-…` |
| `audio/speech` `mp3` | `Content-Type: audio/mpeg`, body starts `ff f3` (frame sync, no ID3) |
| `GET /generation?id=…` | `api_type: tts`, `total_cost: 1.86e-06`, available immediately |
| multipart with `file=@clip.bin` | **400** — the format is inferred from the filename extension |
| multipart with `file=@clip.mp3` | 200, `{"text":"Hi.","usage":{"seconds":1,"cost":0.0001}}` |
| base64 `input_audio` | 200, the same body |
| `verbose_json` + `word` | 200; `duration`, `language:"english"`, `words[]`, **no `segments`** and no token counts |

1. **Multipart for transcription, not base64 JSON.** OpenRouter documents both
   and both answer, but multipart is the shape every OpenAI-compatible server
   accepts, including the local engines of #86, and it does not inflate a 25 MiB
   clip by a third. The filename **must** carry the format extension
   (`clip.mp3`), because the provider infers the codec from it: the same bytes
   as `clip.bin` are a 400. Polaris and her-go both use base64 JSON; base64
   remains the path for files over the 25 MiB multipart cap on OpenAI and Groq,
   and arrives with `voice/openai` if a consumer needs it.
2. **No `provider` passthrough.** OpenRouter does not apply `order`, `only` or
   `ignore` to the audio endpoints, so Polaris's `{"only": ["Together"]}` pin
   cannot work on the current API. A model-level fallback in the role table is
   the resilience lever instead.
3. **Speech cost is a best-effort second request.** The speech response is raw
   audio with no usage block, so exact cost is only available from
   `GET {base}/generation?id=<X-Generation-Id>`, which answered immediately with
   `total_cost` for a Kokoro generation. When cost lookup is on, the adapter
   makes that call with a 5 s bound and fills `Usage.CostUSD`; a failed or slow
   lookup leaves cost at zero and never fails the synthesis. This replaces the
   per-million-character table Polaris maintains by hand, which prices
   characters at the per-token rate and so overestimates prose by roughly the
   chars-per-token ratio (about 4×).

`voice/openrouter.Config.CostLookup` defaults to **false**: a voice agent that
calls `Speak` on every reply should not pay an extra round trip for a number it
never reads. Nova's own front doors set it from `voice.cost_lookup`, which
defaults to true, because they are the ones that report spend.

Errors are `*provider.Error` with the status and `provider.Classify`'s
retryability, the body truncated to 500 characters, and the request bounded by
`ctx`. A non-200 body is JSON; a 200 body is audio, and a body over
`MaxClipBytes` is an error rather than a truncated clip.

## Catalog

`provider.Model` gains one field, `Voices []string`, and two predicates:

```go
func (m Model) SupportsSpeech() bool        { return slices.Contains(m.OutputModalities, "speech") }
func (m Model) SupportsTranscription() bool { return slices.Contains(m.OutputModalities, "transcription") }
```

The live rosters fill `Voices` from `supported_voices`, so an app can offer a
voice picker without a hardcoded list: Kokoro alone publishes 54 voices, against
the 4 hand-written in Polaris. As with decisions and embeddings, no speech or
transcription model is added to the static chat catalog or to `list_models`: the
roster moves weekly and the static list is the chat list.

## Front doors

| Where | Speech | Transcription |
|---|---|---|
| Library | `voice.Speaker` | `voice.Transcriber` |
| Tool | `tool.Context.Speak` | `tool.Context.Transcribe` |
| Agent config | `agent.Config.Speak` | `agent.Config.Transcribe` |
| Protocol | `speak` command, `speech` request field | `transcribe` command, `transcription` request field |
| Extension | `model:speech` capability, `speak` frame | `model:transcribe` capability, `transcribe` frame |
| Extension Go API | `sdk.Host.Speak` / `Env.Speak` | `sdk.Host.Transcribe` / `Env.Transcribe` |
| CLI | `nova speak` | `nova transcribe` |

`tool.Context` and `agent.Config` mirror `Embed` and `Decide` exactly, so an
in-process tool can speak or transcribe and report the call with `AddUsage`.
`agent/sub` must pass both through to a child beside `Complete` and `Decide`;
the same pass-through for `Embed` is missing there today and is fixed separately
in [#87](https://github.com/AutumnsGrove/nova/issues/87), so the voice slice adds
three fields to that struct rather than two. There is no TUI slash command: the
TUI cannot play or record, so a `/speak` that only lists roles would be a menu
with no meal. A voice UI is a separate issue.

Both protocol commands block and are not runs, so they emit no run events, the
same rule `embed` and `decide` follow. Audio crosses the protocol as base64
(`Clip.Bytes` marshals that way), and a per-extension cap of 20 calls per run
(`extensions.speech_max_per_run`, `extensions.transcribe_max_per_run`) bounds a
subprocess that finds a loop.

```go
// in a tool or a library consumer:
clip, err := speaker.Speak(ctx, voice.SpeechRequest{Model: "narrate", Input: text})
if err == nil {
    tctx.AddUsage(clip.Usage)
    wav := voice.WAV(clip) // nil for an mp3 clip
}
```

### The protocol line limit

A `speak` response carries the whole clip, and base64 inflates it by a third: a
40 MiB clip is a 53 MiB JSON line. The documented 16 MiB line limit therefore
cannot carry the largest clip the library allows, so **the limit rises to 64
MiB** (`proto.defaultMaxLine`). The reader already grows its buffer on demand, so
a small frame costs nothing; the larger ceiling is paid only when a large clip
actually crosses. This is a relaxation of the v1 framing contract, recorded as a
decision, and it applies to every frame, so a hostile 64 MiB line from an
authenticated client is possible — the same trust boundary `serve` already has.

`MaxClipBytes` stays at 40 MiB: a 64 MiB ceiling leaves room for the envelope,
and 40 MiB of MP3 at Kokoro's measured ~42 kbps is over two hours of speech.

### CLI

`nova speak` and `nova transcribe` load the config and build only the voice
clients (`mode.Voice`), not an agent server, so they start without a session,
tools, or the extension host. The model flags exist because a script may want a
different voice than the config's role.

```
nova speak "hello" -o hello.mp3 --role narrate
nova speak --model openai/gpt-4o-mini-tts-2025-12-15 --voice alloy -o - < answer.md
nova speak -f answer.md -o answer.mp3          # chunks and joins by default
nova speak "hi" --format pcm -o - | aplay -f S16_LE -r 24000 -c 1
nova transcribe memo.webm --language en
nova transcribe memo.webm --role default --timestamps word --json
```

`nova speak` chunks long input with `voice.Chunks` and joins the clips with
`voice.Join`, so "read this file aloud" is one command. `--chunk-size` sets the
rune count (default `voice.ChunkRunes`); `--no-chunk` refuses an input over the
cap instead. Each chunk is a billed call, so the command prints the chunk count
and the summed cost when it finishes, and a failure in any chunk writes no file
and names the chunk that failed: a partial file that looks complete is worse than
none. `--format pcm` writes one WAV header over the joined samples.

`-o` defaults to `-` (stdout). Writing audio to a terminal is refused with a
message rather than a screenful of bytes; `-o file` or a pipe is required.
`--json` prints the clip or transcript as JSON with the audio omitted (a clip
carries its metadata and byte count, not a base64 blob). `--model` and `--voice`
win over `--role`; with neither, the config's `voice.speech.default` /
`voice.transcribe.default` role is used, and with no config at all the command
errors with the configured role names.

## Test matrix

Rows are named for the test that covers them (`TestSpeak_A02_...`), the way the
solved areas do. `matrixcheck` only reads `docs/archive/chunks/`, so these rows
are a contract between this document and the tests, not a gate.

| Id | Behaviour |
|---|---|
| A01 | A speech request with an empty input, empty model, unknown format or out-of-range speed is refused locally, before any request |
| A02 | An mp3 response yields the bytes, `audio/mpeg`, the configured model and voice, and the generation id |
| A03 | A pcm response with `rate=24000;channels=1` fills the layout, and `WAV` produces a header whose fields and length match the samples |
| A04 | A pcm response with no layout parameters falls back to 24 kHz mono 16-bit |
| A05 | A non-200 response is a `*provider.Error` with the status and retryability; the body is truncated |
| A06 | A response over `MaxClipBytes` is an error, not a partial clip |
| A07 | A role resolves model, voice, format and speed; a direct id with a slash passes through and needs its own voice |
| A08 | A fallback uses its own model and voice, and a fallback whose format the model cannot produce is refused |
| A09 | A `-latest` or `~` id is refused unless `allow_aliases` is set |
| A10 | A cost lookup fills `CostUSD` with source `provider`; a failing or slow lookup leaves zero and still returns the clip; `CostLookup` false makes no request |
| A11 | A cancelled context returns `ctx.Err` with no clip, and the body is closed |
| A12 | A transcription is sent as multipart with `file` **named `clip.<format>`**, `model`, `language`, `temperature` and the response format, and the recorded form fields match |
| A13 | A json transcript maps text and cost into `Transcript` (`CostSource: "provider"`), trims the text, and tolerates a usage block with no token counts |
| A14 | A verbose transcript maps duration, language, segments and words; a word-only response yields words and no segments, and a name like `"english"` is kept as reported |
| A15 | An empty or whitespace-only transcript is a successful response with an empty string, not an error |
| A16 | Audio over `MaxAudioBytes`, an empty audio body, or an unknown format is refused locally |
| A17 | `Timestamps` without `verbose_json` is refused locally |
| A18 | A transcription role falls back once, and the usage of both attempts is accumulated |
| A19 | `List(KindSpeech)` and `List(KindTranscription)` send the matching `output_modalities` filter and map `supported_voices` |
| A20 | `Chunks` splits at sentence boundaries under the cap, hard-splits a long sentence at a space, never splits a rune, and returns nil for blank input |
| A21 | `WAV` returns nil for a non-PCM clip and for an unknown layout, and is byte-exact for a known one |
| A22 | `Join` concatenates same-format clips and sums usage, and a joined mp3 is one playable stream; it refuses mixed formats, mixed PCM layouts and zero clips |
| A23 | The `speak` protocol command returns base64 audio that decodes to the same bytes, and `transcribe` returns the transcript |
| A24 | A frame of 20 MiB is accepted by the protocol reader, and one over 64 MiB fails with `ErrLineTooLong` |
| A25 | An extension without `model:speech` is refused, and the 21st call in a run hits the cap |
| A26 | A tool calling `tctx.Speak` reports the clip's usage into the run total, and a child agent inherits the seam |
| A27 | `nova speak` writes a file, refuses a TTY without `-o`, and `--model`/`--voice` win over `--role` |
| A28 | `nova speak` chunks a long file, joins the clips, reports the chunk count and cost, and writes nothing when a middle chunk fails |
| A29 | `nova transcribe` prints the text, and `--json` prints the full transcript |
| A30 | Fuzz: `Chunks` never panics, never splits a rune and never exceeds the cap; a hostile transcript body never panics the decoder |

## Budgets

Two new packages, and the packages that gain a seam. The raises are deliberate
(D37): `mode` has 16 code lines of headroom today and `cmd/nova` has 4, so both
need room before a line is written.

| Package | Now | After |
|---|---|---|
| `voice` | — | ≤ 520 (contract, roles, chunks, join, wav; measured at 513) |
| `voice/openrouter` | — | ≤ 620 (four endpoints plus the wire helpers; measured at 600) |
| `testkit` | 1200 | 1260 (the scripted voice server) |
| `provider` | 600 | 620 (`Voices`, two predicates) |
| `proto` | 600 | 600 (one constant; the line-limit change is a value, not code) |
| `config` | 1540 | 1640 (`voice` block) |
| `tool` | 660 | 680 (two context fields) |
| `agent` | 1420 | 1430 (two config fields, two pass-throughs) |
| `mode` | 2800 | 2920 (handlers, commands, `mode.Voice`) |
| `mode/providers` | 320 | 400 (three builders) |
| `ext` | 3100 | 3160 (two capabilities, two frames, two caps) |
| `ext/sdk` | 700 | 740 (`Speak`, `Transcribe`) |
| `cmd/nova` | 1260 | 1420 (two subcommands) |

The two new packages also need their budget line in the nearest archived spec
(`C01-foundations.md`, which already budgets `provider`, `roles`, `decide` and
`embed`) as well as in [`ARCHITECTURE.md`](ARCHITECTURE.md).

## Slices

Each slice is one commit with its tests, and this document changes with it.

1. `provider.Model.Voices`, `SupportsSpeech`, `SupportsTranscription`; catalog test.
2. `voice`: the contract, validation, `Chunks`, `Join`, `WAV`, and the roles decorator.
3. `voice/openrouter`: both endpoints, the cost lookup, the roster.
4. `proto`: the 64 MiB line limit and its test.
5. `config`: the `voice` block and its warnings.
6. `tool.Context` and `agent.Config` seams; `agent/sub` passes all three
   through (the missing `Embed` is #87, a separate commit).
7. `mode/providers` builders, `mode` handlers, `mode.Voice`.
8. `ext` capabilities and caps, `ext/sdk`.
9. `cmd/nova speak` and `cmd/nova transcribe`.
10. Docs: `PROTOCOL.md`, `EXTENSIONS.md`, `ARCHITECTURE.md`, `INTEGRATION.md`,
    `DECISIONS.md`, `CHANGELOG.md`.

## Decisions to record

Each of these gets a row in [`DECISIONS.md`](DECISIONS.md) in the slice that
makes it true:

- One `voice` package holds both directions; the two contracts are small and
  share the formats, caps, catalog and client.
- A speech role's fallback restates its own model and voice, because a voice
  name is model-specific.
- Chunking, joining and the WAV header live in the contract; markdown stripping
  does not.
- Speech cost is a best-effort `/generation` lookup, off in the library and on in
  Nova's front doors; a failed lookup is not a failed synthesis.
- Transcription uses multipart in v1; base64 JSON waits for a consumer that
  needs over 25 MiB on a non-multipart provider.
- The audio endpoints get no `provider` passthrough, because the API does not
  apply routing preferences to them.
- The protocol line limit rises from 16 MiB to 64 MiB, because a 40 MiB clip is
  a 53 MiB base64 line and the library's cap should be reachable.
- `nova speak` chunks and joins by default, because a model's input cap is far
  below Nova's guard and "read this file aloud" should be one command.
- There is no TUI entry point: a terminal is not an audio device.
