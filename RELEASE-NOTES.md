# v0.1.7 — release candidate (not published)

This release parses SVG `fill-rule` so compound paths can preserve holes, such
as the interior of the Rust gear logo, when paired with a supporting scanner.

- Parse `nonzero`, `evenodd`, `inherit`, `initial` and `unset` through the
  existing presentation-attribute, class and inline-style pipeline.
- Preserve inheritance and child overrides. Invalid declarations respect the
  configured parser error mode and do not overwrite an earlier valid value.
- Add regressions for declaration precedence, inheritance and invalid values.

## Rasterizer dependency

Standalone builds now replace `github.com/srwiley/rasterx` with the published
`github.com/neomantra/rasterx v0.1.1`, which implements even-odd winding.
Consumers such as ntbrowser must update both their oksvg requirement and their
own rasterx replacement: Go ignores replacement directives in dependencies.

## Validation

An end-to-end pixel regression checks inherited and inline even-odd holes,
nonzero/default fills, and winding reset between paths at native and 2x size.

Three golden images were refreshed for the fork's existing rasterization:
`gradient_userspace@1x`, `pattern_fill@1x` and `pattern_fill@2x`.
The same three comparisons failed with the published rasterx v0.1.0 fork,
with identical mismatch counts and maximum deltas against upstream goldens.
Pattern output is byte-identical between fork versions; the gradient changes
only four pixels by one channel level, within the existing tolerance.
Visual review showed no structural changes. Golden tolerances are unchanged.

Release target: tag the reviewed **nm** branch as `v0.1.7` on **origin**
(`NimbleMarkets/oksvg`). No release tag has been created yet.
