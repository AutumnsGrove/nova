# Splash mockups

Two self-contained HTML pages, drawn with a canvas that paints terminal cells exactly (one cell is 10 by 20 px,
block and line glyphs are drawn as shapes, not font glyphs). Open them in a browser; no build step, no network
except the Google Fonts stylesheet for captions.

| File | What it is |
|---|---|
| `nova-splash-sequence.html` | **The chosen design.** Slash wordmark on the left, a star on the right that ignites as the name slides in, twinkling stars everywhere, and a held end state. The reference for chunk [C21](../../chunks/C21-splash-sequence.md). |
| `nova-logo-workshop.html` | The five directions the choice came from (A block, B slash, C outburst, D the O, E quiet star), with small-size lockups. Kept for the record. |
| `held-frame.png` | A screenshot of the held end state of the sequence. |

In `nova-splash-sequence.html`, the `sequence` function is the reference for the timeline, the shell, the spikes,
the shimmer and the star field. The Playback buttons choose "Once, then hold" (what Nova does), "Loop", or
"Freeze". The "Order" toggle (outburst first) is exploratory and is not part of the spec.

These files are design references, not product code. C21's matrix is normative where it differs from them.
