# v0.1.7 — release candidate (not published)

This release parses SVG `fill-rule` so compound paths can preserve holes, such
as the interior of the Rust gear logo, when paired with a supporting scanner.

- Parse `nonzero`, `evenodd`, `inherit`, `initial` and `unset` through the
  existing presentation-attribute, class and inline-style pipeline.
- Preserve inheritance and child overrides. Invalid declarations respect the
  configured parser error mode and do not overwrite an earlier valid value.
- Add regressions for declaration precedence, inheritance and invalid values.

## Dependency prerequisite

The parser fix alone cannot repair an old ScannerGV whose winding switch is a
no-op. Before publication, after rasterx v0.1.1 is available:

1. Add `replace github.com/srwiley/rasterx => github.com/neomantra/rasterx v0.1.1`
   to go.mod, run `GOWORK=off CGO_ENABLED=0 go mod tidy`, and commit the module
   changes. Do not point this release at an unpublished version or local path.
2. Run build, vet and the full tests with GOWORK=off, including an end-to-end
   even-odd hole pixel regression against the published scanner.
3. Publish the reviewed **nm** branch and tag `v0.1.7` to **origin**
   (`NimbleMarkets/oksvg`).

Consumers such as ntbrowser must update both their oksvg requirement and their
own rasterx replacement: Go ignores replacement directives in dependencies.
Branch pushes are separate from release publication; no release tag has been created.

## Validation before publication

Standalone tests and vet pass with `GOWORK=off CGO_ENABLED=0`.
Testing oksvg with the local rasterx candidate produces three golden failures:
`gradient_userspace@1x`, `pattern_fill@1x` and `pattern_fill@2x`.
All three also fail with the published rasterx v0.1.0 fork, with identical
mismatch counts and maximum deltas against the upstream-scanner goldens.
The pattern outputs are byte-identical between fork versions; the gradient
changes only four pixels by one channel level, within the golden tolerance.
These baseline differences must be reviewed when oksvg adopts the fork;
the combined full suite is not yet green. Existing goldens were preserved.
