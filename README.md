# helm-deep-pack

`helm-deep-pack` collects the container images used by a Helm chart into a
portable bundle. Move the bundle to another machine or environment, then push
the images to a registry with the push helper included in the bundle.

This is useful for:

- mirroring public chart images into a private registry;
- preparing images before an air-gapped or disconnected deployment; and
- promoting the same chart images between development, staging, and production.

## Install

### macOS and Linux

```bash
curl -fsSL https://raw.githubusercontent.com/infinityOrga/helm-deep-pack/main/deploy/install.sh | sh
```

Install a specific release by setting `HELM_DEEP_PACK_VERSION`:

```bash
curl -fsSL https://raw.githubusercontent.com/infinityOrga/helm-deep-pack/main/deploy/install.sh | HELM_DEEP_PACK_VERSION=v1.2.3 sh
```

The installer puts the binary in `/usr/local/bin` by default. Set
`HELM_DEEP_PACK_INSTALL` to choose another directory.

### Windows

Run this in PowerShell:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -Command "iwr -useb https://raw.githubusercontent.com/infinityOrga/helm-deep-pack/main/deploy/install.ps1 | iex"
```

To install a specific release:

```powershell
$env:HELM_DEEP_PACK_VERSION = "v1.2.3"
iwr -useb https://raw.githubusercontent.com/infinityOrga/helm-deep-pack/main/deploy/install.ps1 | iex
```

The Windows installer adds the default install directory to your user PATH.
Open a new terminal after installing, then check the installation:

```console
helm-deep-pack --version
```

Release binaries are also available on the
[GitHub Releases page](https://github.com/infinityOrga/helm-deep-pack/releases).

## Quick start: create, move, and push a bundle

Run `pull` on a machine that can access the chart and its source images:

```bash
helm-deep-pack pull prometheus-node-exporter --repo https://prometheus-community.github.io/helm-charts --output-dir ./node-exporter-bundle
```

The output directory contains the staged images and a standalone `push_images`
helper. Copy the whole directory to the machine that can access the destination
registry, then run the helper from inside the bundle:

```bash
cd node-exporter-bundle
./push_images registry.example.com/team --all
```

On Windows, run `.\push_images.exe registry.example.com/team --all` instead.
The helper is already included in the bundle, so the destination machine does
not need the full `helm-deep-pack` installation. Omit `--all` in an interactive
terminal to choose which images to push.

`--all` is especially useful for automation:

```bash
./push_images registry.example.com/team --all --concurrency 8
```

## Pull images from a chart

```text
helm-deep-pack pull CHART [flags]
```

`CHART` can be a chart name, a local chart directory, or an OCI chart reference.
Use `--repo` for a chart repository; it is not needed for local or OCI charts.

### Public chart repository

```bash
helm-deep-pack pull prometheus-node-exporter --repo https://prometheus-community.github.io/helm-charts --version 4.0.0 -o ./node-exporter-bundle
```

### Local chart with environment-specific values

```bash
helm-deep-pack pull ./charts/my-app -f values-prod.yaml --set image.tag=1.27.1 -o ./my-app-bundle
```

Pass `--values`/`-f` more than once when several values files are needed. Use
`--set` for small overrides without editing a file.

### OCI chart

Authenticate first if the chart is private:

```bash
helm registry login registry.example.com
helm-deep-pack pull oci://registry.example.com/charts/my-app --version 1.2.3 -o ./my-app-bundle
```

### Useful `pull` flags

| Flag | Use it to |
| --- | --- |
| `-r, --repo URL` | Select a remote Helm chart repository. |
| `-v, --version VERSION` | Pin the chart version. |
| `-o, --output-dir DIR` | Choose the bundle directory. |
| `-f, --values FILE` | Load values from a YAML file; repeat as needed. |
| `--set KEY=VALUE` | Override a chart value; repeat as needed. |
| `-c, --concurrency N` | Download more images in parallel; the default is `4`. |
| `--destination-platform OS/ARCH` | Stage a helper for another platform, such as `windows/amd64`. |
| `--rendered-only` | Only collect images from the selected render and chart annotations. |
| `-V, --verbose` | Show additional diagnostic logging. |
| `-k, --allow-insecure-http` | Allow an intentionally plain-HTTP chart repository. |

If `--output-dir` is omitted, a new directory is created in the current
directory.

## Add images manually

Use `add` when a chart references an image that is not present in the bundle, or
when you need to include an image that does not come from the chart:

```bash
helm-deep-pack add quay.io/example/sidecar:1.2.3 busybox:1.36 --output-dir ./my-app-bundle
```

Run `pull` first. `add` updates the existing bundle and skips images that are
already there.

Useful flags:

```text
--output-dir DIR    Existing bundle to update
--concurrency N     Number of images to download in parallel (default: 4)
--verbose           Show additional diagnostic logging
```

## Push images

The preferred portable workflow is to run the helper inside the bundle:

```bash
cd ./my-app-bundle
./push_images registry.example.com/team --all
```

If `helm-deep-pack` is installed on the push machine, the same operation can be
run through the main CLI:

```bash
helm-deep-pack push registry.example.com/team --input-dir ./my-app-bundle --all
```

The registry may include a namespace path, for example
`registry.example.com/team/platform`. Images are written below that path.

In a terminal, omit `--all` to review the staged images and select what to push.
For CI or other non-interactive environments, provide the registry explicitly
and use `--all`. You can also omit the registry in an interactive terminal and
enter it when prompted.

### Useful `push` flags

| Flag | Use it to |
| --- | --- |
| `-i, --input-dir DIR` | Point to a bundle when you are not running inside it. |
| `-a, --all` | Push every staged image without an interactive selection. |
| `-c, --concurrency N` | Push more images in parallel; the default is `4`. |
| `-V, --verbose` | Show additional diagnostic logging. |
| `-k, --allow-insecure-http` | Use an intentionally plain-HTTP registry, such as a local test registry. |

Registry connections use HTTPS by default. Authenticate before pushing with the
credential tool used by your registry, for example:

```bash
docker login registry.example.com
```

Use the same login on the machine that pulls private images. For private OCI
charts, use `helm registry login` before `pull` as shown above.

Do not use `--allow-insecure-http` for a production registry.

## Air-gapped workflow

The bundle lets the pull and push happen on different machines:

```bash
# Connected machine: render the chart and download its images.
helm-deep-pack pull ./charts/my-app -f values-prod.yaml -o ./my-app-bundle

# Transfer ./my-app-bundle to the restricted environment.

# Machine with registry access: push using the included helper.
cd ./my-app-bundle
./push_images registry.internal:5000/platform --all
```

If the push machine uses a different operating system or architecture, set
`--destination-platform` during `pull`, for example
`--destination-platform windows/amd64`.

## Upgrade

Update an installed binary to the latest stable release:

```bash
helm-deep-pack upgrade
```

Skip the confirmation prompt or choose a version explicitly:

```bash
helm-deep-pack upgrade --yes
helm-deep-pack upgrade --version v1.2.3 --yes
```

## Get help

```bash
helm-deep-pack --help
helm-deep-pack pull --help
helm-deep-pack add --help
helm-deep-pack push --help
helm-deep-pack upgrade --help
```
