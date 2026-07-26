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
- **Push engine** — `internal/push`: the registry-transfer logic (archive, probe,
  interactive selection, concurrent push) shared by both the CLI `push` subcommand
  and the standalone push binary.
- **Self-copy fallback** — Legacy/dev staging behavior where the push helper is a
  byte copy of the running CLI. No longer used by the pull staging path.
