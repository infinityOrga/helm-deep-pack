# ADR-0001: Stage a standalone, embedded push helper into pull bundles

## Status
Superseded

## Date
2026-06-26

## Superseded by
Release-hosted `push_images` helper staging (downloaded at pull time per destination platform).

## Context
`pull` produces an **output bundle** (an OCI layout, `push_images.json`, the chart
archive, and a push helper) that an operator runs later to push images into a
target registry. Today the staged **push helper** is a byte-for-byte copy of the
running `helm-deep-pack` binary (`os.Executable()` -> `push_images`). That helper
carries the entire Helm SDK, the chart renderer, and the upgrade machinery even
though pushing only needs the registry-transfer engine.

Measured (release flags: `CGO_ENABLED=0 -trimpath -ldflags "-s -w"`):

- Full `helm-deep-pack`: ~42 MB
- Push-only build (`internal/push` + `internal/pushspec` + `internal/validation`): ~7.2 MB
- Savings per bundle: ~35 MB (~83%)

This matters for bundle transport/bootstrap speed, artifact-size limits, and
repeated transfers across CI / edge / air-gapped relay paths, even though the
container images themselves are typically much larger.

## Decision
Build a dedicated, push-only `push_images` binary and stage *that* (instead of a
self-copy) into every pull bundle. Resolved sub-decisions from the design grilling:

1. **Sourcing — embed (`go:embed`).** A prebuilt `push_images` is embedded into
   `helm-deep-pack` at release time; `pull` writes the embedded bytes into the
   bundle. Keeps a single distributable CLI (no sibling files, no network at pull
   time). The CLI grows by ~7 MB.
2. **Platform — host OS/arch only.** Each `helm-deep-pack` build embeds only the
   `push_images` for its own OS/arch. The staged helper runs on a machine matching
   the puller's platform. (Cross-platform staging is explicitly out of scope.)
3. **Main `push` subcommand — kept.** `helm-deep-pack push REGISTRY` remains. It
   costs ~zero extra binary size because the push engine is already linked through
   `pull`. The standalone helper is additive, not a replacement.
4. **Missing embed — graceful fallback.** Dev/CI builds compile against a committed
   placeholder. When the embed is absent/placeholder, `pull` falls back to the
   current self-copy behavior, so `go build ./...` / `go test ./...` / `go run`
   keep working without any release tooling.

### Key implementation constraint: no recursive embed
The standalone `push_images` imports the push **engine** (`internal/push`). The
embedded bytes must therefore live in a package the engine does **not** import,
otherwise the standalone would embed a copy of itself (7 MB -> 14 MB and growing).
The embed package (`internal/pushbin`) is imported only by the CLI/`pull` side,
never by `internal/push`.

### Staged-helper invocation
The staged helper is invoked with only the registry argument and no subcommand:

```
./push_images REGISTRY [--all] [--input-dir DIR] [--concurrency N] [--allow-insecure-http] [--verbose]
```

It defaults `--input-dir` to its own directory (existing resolution: executable
dir -> cwd), so `./push_images REGISTRY` "just works" from inside a bundle.

## Alternatives Considered
- **Sibling binary in the install dir** — simplest build, but breaks when the CLI
  is moved or run via `go run`, and complicates install/upgrade scripts.
- **Download `push_images` from the GitHub release at pull time** — no CLI bloat,
  but requires network during `pull`, which defeats air-gapped pulls feeding
  air-gapped pushes.
- **Embed all platforms** — enables cross-platform bundles but adds ~35 MB to the
  CLI; rejected because bundles are produced and consumed on matching platforms in
  the target workflow.
- **Remove `helm-deep-pack push`** — rejected; keeping it is free and convenient.

## Consequences
- The release pipeline gains a two-stage build: build `push_images` for the target
  OS/arch, write it to the embed path, then build `helm-deep-pack` embedding it.
- A committed placeholder keeps everyday Go workflows green; only release builds
  produce tiny helpers.
- Bundles shrink by ~35 MB; the distributable CLI grows by ~7 MB.
- Two thin CLI front-ends (`helm-deep-pack push` and `push_images`) share one
  command builder to avoid behavior drift.
