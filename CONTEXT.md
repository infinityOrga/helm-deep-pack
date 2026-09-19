# Project Context: helm-deep-pack

Glossary of domain terms. Use these exact terms in code, issues, and docs; avoid
drifting to synonyms.

## Glossary

- **Pull phase** — Renders a Helm chart, extracts referenced container images, and
  stages them into an output bundle. Implemented in `internal/pull`.
- **Push phase** — Transfers the staged images from an output bundle to a target
  registry. Implemented in `internal/push`.
- **Output bundle** (or **bundle**) — The directory produced by a pull: the OCI
  layout, `push_images.json`, the chart archive, and a push helper. Self-contained
  unit handed to an operator (often across an air gap) to perform the push.
- **Push helper / staged push binary** — The executable placed inside a bundle so
  it can be run later to push images. Named `push_images` (`push_images.exe` on
  Windows).
- **Standalone push binary** — The dedicated, push-only build of `push_images`
  (engine + spec + validation, no Helm/render/upgrade). ~7 MB vs ~42 MB for the
  full CLI. This is what the bundle's push helper should be.
- **Release-hosted push binary** — The standalone `push_images` artifacts published
  in GitHub releases and downloaded by `pull` for the selected destination
  platform before staging into the bundle.
- **Push manifest** — `push_images.json`, the on-disk contract between phases
  (`internal/pushspec`): the OCI layout dir name plus per-image source/target/digest.
  An image may also carry an `optional` marker and best-effort `optionalFlags`
  Helm value paths; older manifests omit these fields and remain readable.
- **Optional image** — An image absent from the normal render, including an
  annotation-only image or an image found only by the synthetic inventory render.
  Required status wins when the same image appears in both inventories.
- **Synthetic inventory render** — A second, inventory-only Helm render that
  enables every effective boolean value while preserving non-boolean values. It
  produces a discoverable union with the normal render; it does not enumerate
  arbitrary Helm value permutations. If a chart's synthetic branches trigger
  chart-authored validation, discovery uses Helm lint mode and reports the
  validation messages; malformed YAML is isolated by skipping only the
  unparseable rendered templates, with a warning. Lint-mode output is
  inventory-only and is not a deployable manifest; the ordinary requested
  render remains strict and authoritative.
- **Optional flag attribution** — Bounded, best-effort render probes that try to
  associate an optional image with the smallest known set of false Helm boolean
  value paths. It is limited by the configured timeout and a 32-render safety
  cap for large charts. Attribution failure never removes the optional marker.
- **Push engine** — `internal/push.Engine`: the image-transfer module (archive,
  probe, interactive selection, concurrent push) shared by both the CLI `push`
  subcommand and the standalone push binary. Package-level functions are default
  entrypoints backed by a new engine instance.
- **Self-copy fallback** — Legacy/dev staging behavior where the push helper is a
  byte copy of the running CLI. No longer used by the pull staging path.
