# SVG text and host font providers

Text rendering and the public `MeasureText` API use one glyph walk: font
selection, combining-mark fallback, kerning, and glyph advances are shared.
This is general SVG functionality; there is no chart-library dependency.

```go
metrics := oksvg.MeasureText("AV café", oksvg.TextStyle{
    Family: "sans-serif", Size: 24, Weight: "700", Style: "italic",
})
fmt.Println(metrics.Advance, metrics.MissingGlyphs)
```

The size is in SVG user units (normally CSS pixels). Measurement is for a raw,
single horizontal text run; it does not perform SVG whitespace normalization,
line wrapping, or shaping. `Ascent`, `Descent`, and `XHeight` describe the selected
primary face; `Advance` and `MissingGlyphs` include fallback glyphs. The parser
reports missing characters in `SvgIcon.MissingGlyphs`. A missing character uses
the chosen base face's .notdef glyph/advance, which some fonts leave blank.

## Host configuration

The library retains its existing process-wide font registry. Configure and
register fonts at host startup, before measurement/parsing; changing registrations
between those operations can change the result. Registry access is synchronized,
but a series of reconfigurations is not a per-document transaction.

A `FaceSource` supplies immutable single-face OpenType bytes for `Regular`,
`Bold`, `Italic`, and `BoldItalic`. `ntfonts.Embedded` implements that interface,
but oksvg itself does not import ntfonts or link its font bundle:

```go
for _, family := range append(ntfonts.Families(), ntfonts.Fallbacks()...) {
    if err := oksvg.RegisterFontFamily(family, ntfonts.Embedded); err != nil {
        return err
    }
}
if err := oksvg.RegisterFontAlias("sans-serif", "IBM Plex Sans"); err != nil {
    return err
}
if err := oksvg.RegisterFontAlias("serif", "Literata"); err != nil {
    return err
}
if err := oksvg.RegisterFontAlias("monospace", "IBM Plex Mono"); err != nil {
    return err
}
return oksvg.ConfigureTextFonts(oksvg.TextFontOptions{
    DisableSystemFonts: true,
    FallbackFamilies: append(ntfonts.Fallbacks(), "IBM Plex Sans", "Literata"),
})
```

Hosts can register an optional CJK provider with `RegisterFontFamily` and put
its family before the bundled fallback names. It is not automatically imported.
Choose the appropriate regional face. Providers with no italic/bold variant can
explicitly return their upright/regular bytes, as the optional SC Regular provider
does. Identical bytes returned for multiple variants share one parsed face.

`RegisterFontFamily` parses all four faces before atomically publishing them.
It copies provider bytes, rejects faces over 32 MiB, and leaves the old registration
unchanged on failure. `RegisterFontAlias` snapshots the currently registered faces
and their bitmap metadata without copying bytes. Register aliases after families.
The older `RegisterFont` and `RegisterFontCollection` APIs remain available.

## Selection and supported layout

* Up to 16 CSS family entries are considered, in order. Each contributes the
  requested variant if registered, otherwise its regular face. Family matching
  is case-insensitive. If no CSS family is registered, sans-serif is the primary.
* Missing glyphs try the rest of the CSS family list, then the configured fallback
  families (at most 32), then Go and Go Mono. A base plus adjacent Unicode marks
  selects a single face covering the whole cluster where possible. Otherwise,
  the first face covering the base is retained and missing marks are reported.
  Leading marks in a later fragment retain the preceding glyph's face; this
  does not retroactively reselect that preceding fragment's face.
* Bold/bolder and numeric weights >=600 select Bold. Italic and oblique select
  Italic; combined styles select BoldItalic. There is no synthetic slant, variable
  font-axis selection, or continuous numeric weight interpolation.
* Without host mappings, sans-serif and serif retain the Go defaults. Monospace
  now uses actual Go Mono, with all four variants. A real serif requires a host
  mapping, such as Literata above. The explicit Go and Go Mono names are available.
* `font-style` and `dominant-baseline` participate in the existing style stack.
  Supported horizontal baselines are alphabetic/auto, middle (half x-height),
  central (half ascent minus descent), text-before-edge, and text-after-edge;
  before-edge/after-edge are aliases. Offsets use primary font metrics and do not
  accumulate as the pen advances across spans. Full OpenType baseline tables,
  vertical writing, hanging/ideographic baselines, and baseline-shift are not
  implemented.
* Existing scalar x/y/dx/dy, text-anchor, whitespace handling, text transforms,
  nested tspan styling, outline glyphs, and sbix bitmap behavior remain supported.

`DisableSystemFonts` prevents automatic system-font loading during both parsing
and measurement. It does not unload fonts already registered, and an explicit
`LoadSystemFonts` call still loads them. Existing callers keep the legacy lazy
system-font loading behavior unless they opt out.

## Limits and validation

This is not a general shaping engine. Arabic joining, bidi ordering, Indic
reordering, OpenType mark positioning/ligatures, and general emoji ZWJ sequences
remain unsupported. Cluster fallback keeps related characters in a font; it does
not supply GPOS/GSUB shaping. The legacy regional-flag/sbix handling is a special
case, not complete emoji support. Text paths, coordinate lists, letter/word
spacing, and textLength/lengthAdjust remain outside this implementation.

Tests cover variants and atomic registration, CSS versus host fallback,
combining clusters, missing counts, measurement/layout agreement, nested style
restoration, baseline offsets, and monospace widths. They run without chart
code or installed-font coverage requirements. A standalone SVG/resvg specimen
in the ntcharts experiment additionally checks Latin, CJK, and unsupported rows
using identical font files. That visual check is not an SVG conformance claim.

`go test -run '^$' -bench BenchmarkSVGText -benchmem` records warm measurement and
parse costs. Glyph-outline caching and shaping integration are subsequent work.
