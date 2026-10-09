# Nova — Brand

The mark is **K3**, locked in on 2026-10-09 after six rounds of studies.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="../assets/logo/nova-mark-dark.svg">
  <img alt="Nova mark" src="../assets/logo/nova-mark-light.svg" width="256">
</picture>

## What it is

A nova is an old, faint star that suddenly flares. The mark is a cell-built
star at the centre of a tilted, two-armed spiral, with a warm companion star at
the end of one arm. The warm companion against the cool primary is the nova
mechanism: a hot body feeding a cooler one.

Every cell is a square on a 49 by 49 grid. That is deliberate: the same artwork
renders as a terminal splash screen (one half-block character per cell) and as
an SVG at any size.

## Files

| File | Use |
|---|---|
| `assets/logo/nova-mark-dark.svg` | On dark backgrounds (terminal, dark README) |
| `assets/logo/nova-mark-light.svg` | On light backgrounds |
| `assets/logo/nova-mark-mono.svg` | One colour: print, stamps, forced-mono contexts |
| `assets/logo/nova-icon.svg` | Rounded tile: app icon, GitHub avatar |
| `assets/logo/gen/main.go` | The generator. Never edit the SVGs by hand. |

Regenerate with `go run ./assets/logo/gen`. The generator is seeded, so the
output is identical on every run.

## Palette

| Role | Dark | Light | Notes |
|---|---|---|---|
| Primary star, first arm (`spark`) | `#62c6ff` | `#0b64c0` | |
| Second arm, dust (`ink`) | `#f2f6fb` | `#101722` | |
| Star centres, companion (`core`) | `#fff4cf` | `#f0a000` | The warm accent. Use sparingly. |
| Icon tile | | `#0b64c0` with `#f4f6f9` cells, `#ffd25a` core | |

The same three roles become the TUI theme's accent, text, and highlight tokens,
so the product and the logo share one palette.

## Rules

- The mark is a raster-like grid on purpose. Do not smooth it, outline it, or
  add gradients.
- The companion star is always the warmest, brightest element.
- Below about 40 px the spiral arms stop reading. A small-size variant (the star
  alone, plus one warm cell) is a separate task for favicons.
- The wordmark is lowercase `nova`, set in a monospace face. In prose, the name is
  `Nova` with only the N capitalised.

## Still to do

- Small-size variant for favicons.
- Terminal splash: half-block rendering of the grid, with a plain-ASCII fallback
  for terminals without truecolor. Specified in [`TUI.md`](TUI.md).
