# oksvg

**This is a fork of [srwiley/oksvg](https://github.com/srwiley/oksvg), developed at
[github.com/NimbleMarkets/oksvg](https://github.com/NimbleMarkets/oksvg) as part of the
NimbleMarkets ecosystem.** It tracks upstream's SVG parsing and rasterization but adds
text/font rendering, color-emoji glyphs, SVG patterns, and a number of correctness and
concurrency fixes described below.

oksvg is a rasterizer for a partial implementation of the **SVG 1.1** specification in Go.
Although many SVG elements are not read by oksvg, it is good enough to faithfully produce
thousands, but certainly not all, SVG icons available both for free and commercially. A list
of supported and unsupported elements is in `doc/SVG_Element_List.txt`.

oksvg uses the [rasterx](https://github.com/srwiley/rasterx) package to rasterize paths,
including the newer 'arc' join-mode.

![arcs and caps](doc/TestShapes.png)

## Install

```
go get github.com/NimbleMarkets/oksvg
```

Import it as:

```go
import "github.com/NimbleMarkets/oksvg"
```

Go modules resolve the rest of the dependency graph (`github.com/srwiley/rasterx`,
`golang.org/x/image`, `golang.org/x/net`) automatically; no manual `go get` of transitive
packages is required.

## What this fork adds

### Text (`<text>` / `<tspan>`)

`<text>` and `<tspan>` are parsed and laid out, including:

* `font-family` (with comma-separated fallback lists), `font-size` (`px`, `pt`, `em`, `rem`,
  `%`), and `font-weight`.
* Per-chunk `text-anchor` (`start` / `middle` / `end`): each new `x`/`y`-positioned run of
  text is measured and anchored independently, matching the SVG text-chunk model.
* `dx` / `dy` relative offsets and kerning (via the font's own kern table).
* SVG whitespace normalization (collapsing runs of whitespace, trimming across fragment
  boundaries) per the spec's default `xml:space="default"` behavior.
* Glyphs missing from the resolved font render as `.notdef` rather than being skipped or
  panicking.

`x`, `y`, `dx`, and `dy` currently accept a single value, not the SVG-legal list-per-character
form (see Known limitations).

### Fonts and font registration

```go
oksvg.RegisterFont("My Custom Font", fontBytes)          // a single-face .ttf/.otf
oksvg.RegisterFontCollection("My Font Family", ttcBytes)  // a .ttc; bare name binds to face 0,
                                                            // individual faces are addressable
                                                            // as "My Font Family-0", "-1", ...
```

A small set of [Go fonts](https://pkg.go.dev/golang.org/x/image/font/gofont) (regular and
bold) are embedded and registered at `init()` as the `sans-serif` / `serif` / `monospace` /
`default` fallback faces, so text renders out of the box with no setup.

System fonts (and, on macOS, Apple Color Emoji) are **not** loaded at import time. Reading
them eagerly would cost every importer of this package real memory whether or not they ever
render text or emoji — Apple Color Emoji's `.ttc` alone is roughly 180 MB. Instead they are
loaded lazily, at most once, the first time a `<text>` element is laid out. If you want to pay
that cost up front (e.g. to avoid a first-render latency spike), call it explicitly:

```go
oksvg.LoadSystemFonts() // safe to call concurrently; a no-op after the first call
```

### Color emoji (sbix bitmap glyphs)

Fonts that carry an Apple-style `sbix` bitmap glyph table (Apple Color Emoji being the
common case) are parsed and cached, and are hardened against malformed/adversarial font data
(no panics on truncated tables, bad offsets, or oversized images).

On macOS, regional-indicator pairs (flag emoji like 🇺🇸) are additionally resolved through
Apple Color Emoji's internal ligature table, which maps each *pair* of regional-indicator
glyphs to a single pre-composed flag glyph — the same thing the OS text engine does. Because
that table's structure is not documented and was reverse-engineered from one specific font
build, this fork gates its use on an exact match of the font's version string (name ID 5,
`"21.4d3e1"` — see `icon_cursor_darwin.go` for the extraction provenance). If the installed
Apple Color Emoji is a different version, flags silently degrade to rendering each
regional-indicator rune as its own glyph (still correct-ish visually, just not a single
composed flag). There is no `morx`/ligature-table parser; the mapping is a fixed, hand-built
table for that one font version.

### Bitmap glyph compositing requires `SvgIcon.DrawTarget`

Color-emoji (and any other bitmap) glyphs are *not* drawn by the vector rasterizer — they are
composited directly onto an `image/draw.Image`. Set `SvgIcon.DrawTarget` to the same image
you're rasterizing paths into, or bitmap glyphs are silently skipped (vector content still
draws normally):

```go
icon, err := oksvg.ReadIcon("emoji-flag.svg", oksvg.WarnErrorMode)
if err != nil {
    log.Fatal(err)
}

w, h := int(icon.ViewBox.W), int(icon.ViewBox.H)
img := image.NewRGBA(image.Rect(0, 0, w, h))

icon.SetTarget(0, 0, float64(w), float64(h))
icon.DrawTarget = img // required for bitmap/color-emoji glyphs to composite

scanner := rasterx.NewScannerGV(w, h, img, img.Bounds())
raster := rasterx.NewDasher(w, h, scanner)
icon.Draw(raster, 1.0)
```

Paths and bitmap glyphs are interleaved in the SVG's document order when drawn, so a path
that overlaps or follows a `<text>` run z-orders correctly against it.

### Patterns (`<pattern>`)

`<pattern>` elements declared in `<defs>` are supported, including:

* `patternUnits` / `patternContentUnits` of `userSpaceOnUse` or `objectBoundingBox`.
* `patternTransform`.
* `fill="url(#id)"` / `stroke="url(#id)"` references, resolved as forward references — a
  `<pattern>` (or gradient) defined later in the document than the element that references it
  still resolves correctly, because `url()` references are fixed up after the whole document
  is parsed.
* Gradient `xlink:href`/`href` stop inheritance (one gradient borrowing another's stops).

Rendered pattern tiles are capped at 4096 px per axis regardless of the SVG's requested
scale, to bound memory use against adversarial input.

### Thread-safety

* **After this fork's concurrency fixes, calling `SvgIcon.Draw` concurrently from multiple
  goroutines on the *same, already-parsed* `SvgIcon` is safe**, including when patterns or
  color-emoji glyphs are involved, as long as each call targets its own raster/`DrawTarget`
  destination (drawing two goroutines into the *same* destination image is a data race at the
  image level, not an oksvg one).
* Parsing (`ReadIcon`/`ReadIconStream`/`ReadReplacingCurrentColor`) and any API that mutates a
  parsed `SvgIcon` (e.g. `SetTarget`, or reaching into its fields) are **not** thread-safe.
  Parse once, then either draw concurrently via `Draw`, or give each goroutine its own parsed
  copy if it needs to mutate icon state.
* `RegisterFont`, `RegisterFontCollection`, and `LoadSystemFonts` are safe to call
  concurrently with each other and with rendering.

## Behavior changes vs `srwiley/oksvg`

This fork intentionally changed a few behaviors from upstream. If you're migrating from
`srwiley/oksvg`, check whether any of these affect your output:

* **Default `stroke-width` is now `1.0`**, matching the SVG specification's initial value.
  Upstream defaulted to `2.0`.
* **`SvgIcon.SetTarget` now correctly accounts for a non-zero `viewBox` origin** (`viewBox="x
  y w h"` with `x`/`y` != 0). Upstream ignored the origin, which shifted content for icons
  using such a viewBox.
* **A `style="..."` attribute now wins over presentation attributes** (e.g. `fill="red"
  style="fill:blue"` renders blue), per the SVG/CSS cascade. Upstream's precedence did not
  consistently honor this.
* **`fill-opacity` / `stroke-opacity` now replace rather than compound** across nested
  elements (an inner element's `fill-opacity` is not multiplied by its ancestors'). Group
  `opacity` still compounds (see Known limitations).
* **`Pattern.GetColorFunction`'s signature changed**: it now takes an `oksvg.ObjectBounds`
  (an axis-aligned `{X, Y, W, H float64}` user-space bounding box) instead of its previous
  parameter shape, so `objectBoundingBox`-unit patterns can be positioned correctly. Code that
  called this directly (most consumers only call `Draw`, which doesn't need to) will need to
  update the call site.
* **Error-mode handling is now consistent for both element and style errors.** `StrictErrorMode`
  aborts the parse on either kind of error; `WarnErrorMode` logs and continues; `IgnoreErrorMode`
  continues silently. Upstream's handling of style errors was inconsistent with its handling of
  unrecognized elements.

## Known limitations

* **No `<image>` element.** Embedding external or data-URI raster images via `<image>` is not
  implemented; only bitmap glyphs sourced from font `sbix` tables are composited.
* **Group opacity is approximated per-primitive, not true group compositing.** A `<g
  opacity="0.5">` multiplies its descendants' own fill/stroke opacity rather than rendering the
  group to an offscreen buffer and compositing that as a whole; overlapping shapes within the
  same semi-transparent group will show seams where upstream SVG renderers would not.
* **`userSpaceOnUse` gradient/pattern coordinates given as percentages are not resolved
  against the SVG viewport.** Percentages are only meaningful for `objectBoundingBox`-unit
  gradients/patterns today.
* **Flag ligatures are pinned to one Apple Color Emoji font version** (see Color emoji,
  above); there is no general `morx`/ligature-table parser, so a future macOS font update
  that changes glyph IDs will fall back to per-rune flag rendering until this fork's table is
  refreshed.
* **`x`, `y`, `dx`, `dy` on `<text>`/`<tspan>` accept a single value**, not the SVG-legal
  space/comma-separated list that positions individual characters.

### Extra non-standard features

In addition to `arc` as a valid `stroke-linejoin` value, oksvg also allows `arc-clip`, the arc
analog of `miter-clip`, plus some extra capping and gap values. It can also specify different
capping functions for line starts and ends.

## Example renderings

Example renderings of unedited open-source SVG files by oksvg and rasterx are shown below.

Thanks to [Freepik](http://www.freepik.com) from [Flaticon](https://www.flaticon.com/),
licensed under [Creative Commons 3.0](http://creativecommons.org/licenses/by/3.0/), for the
example icons shown below, which are also used as test icons in the `testdata` folder.

![Jupiter](doc/jupiter.png)

![lander](doc/lander.png)

![mountains](doc/mountains.png)

![bus](doc/school-bus.png)

## License

BSD 3-Clause. See [LICENSE](LICENSE).
