# helm-deep-pack

`helm-deep-pack` renders Helm charts, finds referenced container images, stages them as OCI artifacts, and pushes them to your target registry.

## Install (latest stable)

macOS/Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/infinityOrga/helm-deep-pack/main/deploy/install.sh | sh
```

Windows (PowerShell):

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -Command "iwr -useb https://raw.githubusercontent.com/infinityOrga/helm-deep-pack/main/deploy/install.ps1 | iex"
```

Installers detect OS/architecture, download the latest stable release from GitHub, and install `helm-deep-pack`:

- macOS/Linux default: `/usr/local/bin` (or `HELM_DEEP_PACK_INSTALL`)
- Windows default: `%LOCALAPPDATA%\Programs\helm-deep-pack\bin` (or `HELM_DEEP_PACK_INSTALL`)

Pin a specific release:

```bash
HELM_DEEP_PACK_VERSION=v1.2.3 curl -fsSL https://raw.githubusercontent.com/infinityOrga/helm-deep-pack/main/deploy/install.sh | sh
```

```powershell
$env:HELM_DEEP_PACK_VERSION="v1.2.3"; iwr -useb https://raw.githubusercontent.com/infinityOrga/helm-deep-pack/main/deploy/install.ps1 | iex
```

## Quick start

Pull images from a chart into an output directory:

```bash
helm-deep-pack pull prometheus-node-exporter \
  --repo https://prometheus-community.github.io/helm-charts \
  --output-dir ./prometheus-node-exporter
```

Push staged images to a registry:

```bash
helm-deep-pack push registry.internal:5000 --input-dir ./prometheus-node-exporter
```

Upgrade in-place to the latest stable release:

```bash
helm-deep-pack upgrade
```

Pin to a specific release:

```bash
helm-deep-pack upgrade --version 1.2.3 --yes
```

## Pull command

```bash
helm-deep-pack pull CHART [--repo REPO] [--version VERSION] [--output-dir DIR] [--concurrency N] [--values FILE]... [--set KEY=VALUE]... [--destination-platform OS/ARCH] [--rendered-only] [--optional-image-timeout DURATION] [--allow-insecure-http]
```

- `CHART` can be a chart name, local chart path, or `oci://...` reference.
- `--repo` is HTTPS by default; use `--allow-insecure-http` only for intentionally plain-HTTP chart repositories.
- Use `--values`/`-f` and `--set` to render deployment-specific variants that expose optional image references.
- By default, `pull` renders the requested values and then performs a best-effort inventory render with every effective boolean value enabled. The bundle contains the union of those renders; this is a discoverable union, not an exhaustive enumeration of arbitrary Helm value permutations.
- Images found only by the inventory render or chart annotations are marked `[optional]` in the interactive push picker and are unselected by default. Known Helm value paths are shown when attribution finishes within the timeout. Required images remain selected only when the operator chooses them, as before.
- `--rendered-only` opts out of synthetic discovery while preserving chart-level image annotation extraction. `--optional-image-timeout` controls best-effort flag attribution and defaults to `15s`; attribution also stops after 32 render probes as a memory guard for very large charts. Use `0` to keep synthetic discovery but skip attribution probes.
- Synthetic discovery retries chart-authored validation guards in Helm lint mode and skips only malformed synthetic template files, so charts such as `kube-prometheus-stack` can still contribute images from valid branches. These and optional-image archive failures are warnings. If an optional image is missing from the bundle, use `add IMAGE...` to stage it explicitly. Required image archive failures remain fatal.
- `--destination-platform` controls which platform helper binary is staged into the bundle (`os/arch`, for example `windows/amd64`). If omitted, it defaults to the current host platform.
- `windows/amd64` is the recommended destination target. `pull` prints a warning whenever the effective destination is not `windows/amd64`, including how to fix it.
- `pull` downloads the destination `push_images` helper from GitHub release assets and verifies it against `checksums.txt`; network access is required at pull time.
- In local dev runs (`go run`, version `dev`), `pull` builds `cmd/pushimages` on demand for the selected destination platform instead of fetching release helper assets.
- Release source defaults are embedded at build time via GoReleaser ldflags. If you fork or transfer the repository, update the build-time values in the release workflow rather than changing runtime config.

Examples:

```bash
helm-deep-pack pull ./charts/my-local-chart
helm-deep-pack pull oci://registry.example.com/charts/mychart --version 1.2.3
helm-deep-pack pull mychart --repo https://charts.example.com -f values-prod.yaml --set sidecar.enabled=true
```

## Add command

```bash
helm-deep-pack add IMAGE... [--output-dir DIR] [--concurrency N] [--verbose]
```

- Adds extra container images to an **existing** pull output directory (run `pull` first).
  It appends them into the OCI layout and updates `push_images.json`.
- Images added explicitly are treated as required images.
- `IMAGE...` are one or more image references, e.g. `nginx:1.27` or `redis@sha256:...`.
- `--output-dir` defaults to the current directory; point it at the dir created by `pull`.
- Images already present are skipped; only new images are fetched and staged.

```bash
helm-deep-pack add busybox:1.36 alpine:3.20 --output-dir ./prometheus-node-exporter
```

## Push command

```bash
helm-deep-pack push [REGISTRY] [--input-dir DIR] [--concurrency N] [--all] [--allow-insecure-http]
```

- `REGISTRY` accepts a host (`registry.internal:5000`) or host plus namespace path
  (`registry.internal:5000/team/sub`). Images are pushed to `<REGISTRY>/<target>`.
- `REGISTRY` is optional: when omitted in an interactive terminal, `push` prompts
  for it (re-prompting until a valid registry is entered). In non-interactive
  contexts (for example CI or piped input), the registry must be supplied as the
  argument, otherwise `push` exits with an error.
- Registry connections use HTTPS by default; use `--allow-insecure-http` only for intentionally plain-HTTP registries (for example local test registries). When the
  target looks like a plain-HTTP registry and you are in an interactive terminal,
  `push` warns and asks whether to continue over HTTP instead of failing outright;
  in non-interactive contexts it still errors and points to `--allow-insecure-http`.

When run in a terminal, `push` is interactive by default so you can choose which images to mirror.

Optional rows include their known Helm value paths and start unchecked. Use the `a`/`--all` behavior when you want to push every staged image, including optional images.

Use `--all` for non-interactive environments (for example CI):

```bash
helm-deep-pack push registry.internal:5000 --input-dir ./prometheus-node-exporter --all
helm-deep-pack push registry.internal:5000/team --input-dir ./prometheus-node-exporter --all
```

If `--input-dir` is omitted, `push` looks for `push_images.json` next to the running executable, then in the current working directory.

## Upgrade command

```bash
helm-deep-pack upgrade [--version VERSION] [--force] [--yes]
```

- Without `--version`, `upgrade` installs the latest stable GitHub release.
- `upgrade` uses the same build-time embedded release source values as `pull`.
- `--version` accepts either `1.2.3` or `v1.2.3`.
- `--yes` skips the confirmation prompt.
- `--force` reinstalls even when already on the selected version.

## Private OCI chart registries

For private OCI registries, authenticate first:

```bash
helm registry login registry.example.com
```

`helm-deep-pack` reuses existing Helm/Docker registry credentials.

## Help

```bash
helm-deep-pack --help
helm-deep-pack --version
helm-deep-pack pull --help
helm-deep-pack add --help
helm-deep-pack push --help
helm-deep-pack upgrade --help
```
