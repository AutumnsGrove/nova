---
chunk: C21
title: "Splash sequence: slash wordmark, outburst, star field"
packages: [./tui/splash, ./tui/ui]
budgets: { ./tui/splash: 700, ./tui/ui: 1950 }
depends: [C16]
---

# C21 — Splash sequence: slash wordmark, outburst, star field

## Goal

The splash screen stops being a downscaled picture and becomes a short animation drawn directly on the
character grid: a slash-filled `nova` wordmark slides in on the left while a star on the right ignites and throws
off an expanding shell of gas, over a field of twinkling stars. When the sequence ends nothing fades; the whole
scene stays on screen, the stars keep twinkling, and the name keeps shimmering until a key is pressed.

The human chose this design on 2026-10-13 from `docs/mockups/splash/`. The current splash renders the 97 by 49
cell mark by averaging cells into half-blocks, which turns its many faint cells into mud at every size a real
terminal offers.

## Read first

- `docs/mockups/splash/README.md`, then `docs/mockups/splash/nova-splash-sequence.html`. Read the `sequence`
  function and the constants above it; they are the reference for every number below. Open the page in a browser
  and watch it with "Once, then hold" selected before writing code.
- `docs/BRAND.md` (palette and rules) and `docs/TUI.md` section "Splash and logo".
- `tui/splash/splash.go` (what is being replaced), `tui/styles/theme.go` and `profile.go` (tokens and colour
  profiles), and `tui/ui/model.go`, `editor.go`, `update.go`, `view.go` (the clock, ticks, `showSplash`,
  `mainView`).
- `docs/TESTING.md` sections 3 to 6.

## Files

| File | Change | Purpose |
|---|---|---|
| `tui/splash/splash.go` (`!notui`) | replace | `Render`, tier choice, the plain fallback, centring |
| `tui/splash/canvas.go` (`!notui`) | create | a cell buffer, `put`, colour blending, and run-coalesced output |
| `tui/splash/font.go` | create | the two letter bitmaps and the slash-cell expansion |
| `tui/splash/sequence.go` (`!notui`) | create | the timeline, star field, shell, spikes, wordmark, shimmer |
| `tui/splash/mark_gen.go` | delete | the generated cell data is no longer used |
| `tui/splash/testdata/*` | replace | new goldens |
| `tui/splash/*_test.go` | replace | tests for the rows below; SPL01 to SPL11 tests are removed (see Retired rows) |
| `tui/ui/model.go`, `editor.go`, `update.go`, `view.go` | change | splash start time, a 66 ms tick while the splash is visible, elapsed time into `Render` |
| `tui/ui/*_test.go` | change | the UI rows below |
| `docs/TUI.md`, `docs/BRAND.md` | change | describe the new splash; drop the cell-grid description |

`assets/logo/gen` is not changed. It still writes the README SVGs. Its `-go` flag has no consumer after this
chunk; leave it and say so in `NEXTSTEPS.md`.

## Interfaces

```go
// package splash

// Tier is how much of the scene is drawn.
type Tier int

const (
	Full    Tier = iota // wordmark, star, outburst, stars: 96 by 21 cells
	Compact             // thin wordmark, small outburst: 64 by 13
	Thin                // thin wordmark and stars, no outburst: 46 by 10
	Lockup              // one-line lockup and a few stars: 40 by 5
	Plain               // text only, no colour, no motion
)

// Sequence is the length of the opening sequence. After it the scene is held.
const Sequence = 8 * time.Second

// Render draws the splash into a width by height area at the given time since
// the splash first appeared. It reports the tier used, and ok false when even
// the plain tier does not fit. Output is exactly height lines, none wider than
// width. It is pure: the same arguments give the same bytes.
func Render(width, height int, st styles.Styles, p colorprofile.Profile, version string, at time.Duration) (out string, tier Tier, ok bool)

// Animated reports whether a tier changes over time. The UI ticks only when it does.
func Animated(t Tier) bool
```

`tui/ui` gains, on `Model`: `splashStart time.Time`, `splashAt time.Duration`, and the constant
`splashTick = 66 * time.Millisecond`.

## Behaviour

**Tiers**

1. The largest tier whose size fits the area is used: Full 96 by 21, Compact 64 by 13, Thin 46 by 10, Lockup 40
   by 5. There is no extra margin; the footer and header are outside the area. Below 40 columns or 5 rows, and
   at 20 columns by 3 rows or more, the Plain tier is used. Under 20 columns or 3 rows `ok` is false.
2. The Plain tier is also used at any size when the colour profile is below `ANSI256` or the theme is `mono`.
   It prints `nova` and, on the next line, `press any key`, centred, with no escape codes and no motion. When a
   version is set, `nova <version>`.
3. A tier's scene is centred in the area (integer division, extra space on the right and bottom). The star
   field fills the whole area, not just the scene.

**Drawing**

4. Every cell is one terminal column. A cell is a glyph and a colour; colour is a theme role blended toward the
   theme background by an opacity, because a terminal has no alpha. Roles are the existing tokens `spark`,
   `ink`, `core`, `warm`, and `bg`. No literal colour appears in `tui/splash`.
5. Glyphs used: `╱ ─ │ · ● ◆ ✦` and the ASCII characters `. : + *`. Each has display width 1.
6. The Full and Compact tiers are only drawn with a profile of `ANSI256` or better. Colours are converted with
   the profile, as the current splash does.
7. Output coalesces runs of cells that share a style into one styled string.

**The wordmark**

8. The word is lowercase `nova`, from the two bitmaps under "Bitmaps" below: bold (6 by 6 pixels
   a letter, one pixel of space) for Full, thin (5 by 5) for Compact and Thin. Each pixel is **2 columns by 1
   row**, which makes a square pixel in a 1:2 cell, and is drawn as `╱╱`.
9. A warm shadow is drawn first: for every word cell with no word cell at (+1, +1), a `╱` at (+1, +1) in `warm`
   at opacity 0.42.
10. The face is shimmered. The colour of a word cell at column `x`, row `y`, time `t` seconds is: a base
    that runs from `spark` toward `ink` across the word (at most 55 percent of the way), blended toward `core`
    by `h`, where `h` is the larger of a travelling highlight and a rare sparkle. The highlight is a Gaussian of
    `d = x + 1.6*y - p`, with `p = (t*18 mod (w+36)) - 12`, `w` the word width in cells, and a standard deviation
    of 5 cells. The sparkle is 0.6 for a cell whose hash of `(x/2, y, floor(3t))` exceeds 0.985. Opacity is
    `0.78 + 0.22*h`, times a per-cell flicker of `0.72 + 0.28*hash(x, y, floor(2t))`. The hash is any fixed,
    deterministic function with a uniform output in [0, 1).
11. **Assembly.** The word is revealed left to right between `0.05` and `0.34` of `Sequence` (a progress
    `rev`, smoothstep). A cell shows when `x/w < rev*1.25`. Cells near the leading edge are blended toward
    `core`, so the edge glows. After `0.34` the word is complete.

**The star and the outburst** (Full and Compact tiers only)

12. The star sits to the right of the word at the positions in the tier table below. Before ignition it is a
    `◆` in `spark` at opacity `0.45 + 0.25*breath`, where `breath = 0.6 + 0.4*sin(2.2 t)`.
13. **Ignition overlaps assembly.** The outburst starts at `0.12` of `Sequence`, while the name is still
    sliding in. With `bu` the progress since then (as a fraction of `Sequence`), the flare is
    `F = 0.1 + 0.9*smoothstep(bu/0.1)` for `bu < 0.1`, and `0.3 + 0.7*(1 - smoothstep(0.1, 0.5, bu))` after.
14. The star is `✦` when `F > 0.5` and `◆` otherwise, coloured from `spark` toward `core` by `F` (toward a
    `core`/`ink` mix above 0.8), at opacity `0.6 + 0.4*F` once ignited.
15. **Spikes.** A horizontal line of `─` and a vertical line of `│` through the star, of length `F*Lh` columns
    and `F*Lv` rows, with opacity `F*(1 - k/len)^1.4` (horizontal) and `^1.2` (vertical) at distance `k`, coloured
    from `core` at the star to `spark` at the tip. They are drawn before the word, so letters cover them.
16. **Shell.** A clumpy shell expands from the star. Radius in rows is `R = smoothstep(0.06, 0.52, bu)*Rmax`
    (a cell's distance is `hypot(dx/2, dy)`, which corrects for the 1:2 cell). Two bands, an outer one at `R`
    and an inner one at `0.6R`, have intensity `exp(-((d-R)^2)/0.8)` and `exp(-((d-0.6R)^2)/1.1)` times an
    angular clump factor (see the reference). Intensity maps to a glyph: above 0.8 `*`, 0.6 `+`, 0.4 `:`, 0.22
    `·`, 0.1 `.`, else nothing. The outer band runs from `spark` toward `warm` as `R` grows, so the shell cools
    from blue to orange; the inner band stays `spark`.
17. The shell fades with `max(1 - smoothstep(0.36, 0.55, bu), 0.3)`. It never goes below 0.3.
18. The **companion** is a `●` in `warm` at opacity 0.9, orbiting the star at angle `1.3*t` with a radius of 4
    columns and 2 rows.

**The held state**

19. At and after `Sequence` the scene is held, not faded: the word is complete and keeps shimmering (behaviour
    10 continues with `t` growing); the star field keeps twinkling; the companion keeps orbiting; the star holds
    at `F = 0.3` (a `◆`, spikes at 30 percent of peak length); the shell holds at radius `Rmax` at opacity
    0.3; the tagline and `press any key` are shown. Nothing is removed. The held scene is the same for any
    `at >= Sequence`, apart from the time-driven terms.

**Stars, tagline, and the thinner tiers**

20. **Star field.** A cell holds a star when a hash of its absolute `(x, y)` in the area is below `0.055`. The
    glyph is one of `· · . +`, chosen by the hash. Opacity is `0.1 + 0.4*(0.5 + 0.5*sin(t*speed + phase))`
    with a per-star `speed` in 0.6 to 2.2 and `phase` in 0 to 2π, both from the hash. Stars are drawn first.
    Because they key on absolute cells, growing the area adds stars and never moves existing ones.
21. **Tagline.** `terminal coding agent` in `ink` at opacity 0.55 on the tier's tagline row, left aligned with
    the word, with the version appended after two spaces in `ink` at 0.35 when it is set. `press any key` on the
    next row in `spark` at opacity `0.4 + 0.2*sin(2.4 t)`. Both fade in between `0.42` and `0.56` of `Sequence`.
22. **Thin** draws the thin word, the shadow, and the stars, with `press any key` centred under the word. The
    shimmer uses speed 12 and a deviation of 4. The word is fully drawn and shimmering from the first frame
    (no assembly), because there is no outburst to overlap.
23. **Lockup** draws `///` (three `╱`, shimmered) and `nova` on one row, centred, with a few stars.

**Tier geometry** (cells, relative to the tier's top-left; `Rmax`, `Lh`, `Lv` as used above)

| Tier | Scene size | Word origin | Star | `Rmax` | `Lh` | `Lv` | Tagline row |
|---|---|---|---|---|---|---|---|
| Full | 96 by 21 | (4, 7) | (76, 10) | 9 | 30 | 9 | 15 |
| Compact | 64 by 13 | (2, 4) | (54, 6) | 5 | 16 | 5 | 11 |

**Bitmaps** (`#` is a pixel)

```
bold (Full)                          thin (Compact, Thin)
n  #####. ##..## ##..## ##..## ##..## ##..##     n  ####. #...# #...# #...# #...#
o  .####. ##..## ##..## ##..## ##..## .####.     o  .###. #...# #...# #...# .###.
v  ##..## ##..## ##..## .####. .####. ..##..     v  #...# #...# #...# .#.#. ..#..
a  .####. ....## .##### ##..## ##..## .#####     a  .###. ....# .#### #...# .####
```

**In the UI**

24. The model records the time the splash is first shown (`splashStart`) and the elapsed time at every tick
    (`splashAt`). `mainView` passes `splashAt` to `Render`.
25. While `showSplash()` is true and the tier is `Animated`, the model keeps one timer chain running at
    `splashTick` (66 ms, about 15 frames a second). It shares the existing `tickMsg` and `ticking` guard, so a
    thinking block's 250 ms tick and the splash tick are never both pending.
26. The first key press, or the first event that fills the transcript, dismisses the splash as today
    (SPL12 to SPL14 are unchanged). After dismissal no splash tick is scheduled.
27. `/display splash off`, a non-empty transcript, and a non-`Animated` tier schedule no splash tick.
28. A resize re-chooses the tier from the new size and keeps `splashAt`, so the scene does not restart.
29. `mark_gen.go` and the `go:generate` line are removed, and `tui/splash` no longer has any generated file.

## Matrix

| ID | Case | Input / setup | Expected |
|---|---|---|---|
| SQN01 | tier by size | areas 120×40, 96×21, 95×21, 64×13, 63×13, 46×10, 45×10, 40×5, 39×5, 20×3, 19×3 | Full, Full, Compact, Compact, Thin, Thin, Lockup, Lockup, Plain, Plain, ok false |
| SQN02 | tier needs both dimensions | 200×20 and 96×20 | Compact (not Full) for both |
| SQN03 | below 256 colours is plain | profile `ANSI`, 120×40 | Plain; no escape codes |
| SQN04 | mono theme is plain | mono theme, truecolor profile | Plain |
| SQN05 | output never exceeds the area | every tier, times 0 to 60 s, odd sizes | exactly `height` lines, each no wider than `width` |
| SQN06 | deterministic | same arguments twice | identical bytes |
| SQN07 | first frame | Full, `at` 0 | stars and a dim `◆` present; no wordmark cell; no spike; no shell |
| SQN08 | name assembles left to right | Full, `at` 0.10, 0.20, 0.30 of `Sequence` | wordmark cell count strictly increases; every drawn cell is left of the leading edge |
| SQN09 | name complete after assembly | Full, `at` 0.34 of `Sequence` | every wordmark and shadow cell is drawn |
| SQN10 | **ignition overlaps assembly** | Full, `at` 0.20 of `Sequence` | the word is partly drawn (some, not all, cells) **and** spike cells and the `✦` or `◆` ignition glyph are present |
| SQN11 | spikes grow then ease | Full, `at` 0.12, 0.22, 0.30 of `Sequence` | horizontal spike length rises to a peak then falls toward the held length |
| SQN12 | shell expands | Full, `at` 0.25 to 0.62 of `Sequence` in steps | the farthest shell glyph from the star never moves closer |
| SQN13 | shell cools | Full, `at` 0.28 and 0.60 of `Sequence` | the outer-band colour is bluer at the first time and warmer (red above blue) at the second |
| SQN14 | star glyph by flare | Full, `at` 0.22 and 0.90 of `Sequence` | `✦` at the first, `◆` at the second |
| SQN15 | held state keeps everything | Full, `at` = `Sequence`, 20 s, 10 min | wordmark complete, shadow, tagline, `press any key`, star, spikes, companion, shell glyphs all present |
| SQN16 | held shell stays faint | Full, `at` 20 s | shell glyphs present at radius `Rmax`; their opacity is the 0.3 floor, not zero |
| SQN17 | the name keeps shimmering | Full, held, `at` 20 s and 20.4 s | wordmark glyphs and positions identical; at least one wordmark cell's colour differs |
| SQN18 | stars twinkle in place | Full, held, 0.5 s apart | star glyph positions identical; at least one star colour differs |
| SQN19 | companion orbits | Full, held, 0.5 s apart | the `●` moves and stays within 4 columns and 2 rows of the star |
| SQN20 | slash glyph and shadow | Full, held | every face cell is `╱`; shadow cells are `╱` in the `warm` role, only where no face cell sits at (+1, +1) |
| SQN21 | tagline and version | Full, held, version `v1.2.3` | `terminal coding agent  v1.2.3` and `press any key` under the word, left aligned with it |
| SQN22 | tagline fades in | Full, `at` 0.30 and 0.60 of `Sequence` | absent at the first, present at the second |
| SQN23 | colours come from the theme | Full with a theme whose `spark` differs | output differs; a scan finds no literal colour in `tui/splash` source |
| SQN24 | profile degradation | Full, profile `ANSI256` | every colour escape is a 256-colour code |
| SQN25 | scene is centred | Full scene in 120×40 | the wordmark origin is at (4 + 12, 7 + 9) |
| SQN26 | stars stable under resize | Full, 100 wide then 101 wide, same row | every star in the 100-wide output is in the 101-wide output at the same cell |
| SQN27 | Compact | 64×13, held | thin word, shadow, star, shell at `Rmax` 5, tagline on row 11 |
| SQN28 | Thin has no outburst | 46×10, any time | thin word complete from `at` 0, no spike, no shell, no `●` |
| SQN29 | Lockup | 40×5 | `///` and `nova` on one row, centred |
| SQN30 | Plain is static | Plain, `at` 0 and 1 hour | identical output |
| SQN31 | glyph widths | every glyph the package can emit | display width 1 |
| SQN32 | goldens | Full at 0, 0.20, 0.50 of `Sequence` and held; Compact held; Thin; Lockup; Plain | match files in `testdata` |
| SQN33 | splash ticks while visible | UI model, empty transcript, Full tier, fake clock | a tick is scheduled at 66 ms; after each `tickMsg` another is scheduled |
| SQN34 | elapsed time follows the clock | advance the fake clock 2 s through ticks | the view equals `Render(..., at: 2s)` |
| SQN35 | dismissal stops the ticks | press a key, then deliver a `tickMsg` | the splash is gone and no further tick is scheduled |
| SQN36 | first event stops the ticks | deliver a transcript-filling event | same as SQN35 |
| SQN37 | no tick when off or not animated | `/display splash off`; a Plain tier; a non-empty transcript | no splash tick scheduled |
| SQN38 | one timer chain | start a thinking block while the splash is gone, and the reverse | at most one tick pending at any time |
| SQN39 | resize keeps the time | resize from Full to Compact at `at` 3 s | the tier changes; `splashAt` is unchanged |
| SQN40 | generated file gone | read the package directory | no `mark_gen.go`; no `go:generate` line in `splash.go` |

## Fuzz and property tests

| Name | Asserts |
|---|---|
| `FuzzRender` | arbitrary sizes, durations (including negative and huge), profiles, versions with wide or control characters: no panic; exactly `height` lines; no line wider than `width` |
| `TestProperty_SQN_Bounds` | a seeded sweep of sizes and times: every cell written is inside the area and the output never changes length |

## Retired rows

This chunk replaces the cell-grid splash, so C16 rows **SPL01 to SPL11** no longer describe the product and are
retired: SQN01 to SQN06, SQN30 to SQN32 and SQN40 cover their intent (levels, bounds, plain fallback,
determinism, goldens, generated file). C16 rows **SPL12 to SPL14** (dismissal, hidden when the transcript is not
empty, resize) stay and must still pass. The implementing agent edits `docs/chunks/C16-tui-rendering.md` to mark
SPL01 to SPL11 `retired by C21` and removes their tests, and lowers or removes C16's `./tui/splash` budget line
so `make ci` stays green. The human approves this by assigning C21; say so in `NEXTSTEPS.md`.

## Out of scope

- The README banner and the favicon. The SVG generator is untouched. Redrawing the banner from the new bitmap
  is a later step.
- The one-glyph header mark `✦`.
- The "outburst first" order, and the loop and freeze modes. They exist only in the mockup.
- Any sound, any mouse interaction, and any setting for reduced motion (see the open question in
  `NEXTSTEPS.md`).
- Checking the glyphs in real terminal fonts. The mockup draws them as shapes; a person should look at the real
  thing in a few terminals before release.

## Acceptance

```
go run ./scripts/accept C21
```

Also run the new fuzz target for ten seconds, and look at the result in a real terminal at 100 by 30 and at 80
by 24 (`nova` with an empty transcript). Note in `NEXTSTEPS.md` what each tier looked like.
