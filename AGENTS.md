# AGENTS.md

## Project

Go CLI for rendering Helm charts, extracting referenced container images, archiving
them into an OCI layout, and pushing them to a target registry.

## Architecture

The CLI is a thin command layer over independently testable workflow packages. The
two lifecycle phases are **pull** (render and stage) and **push** (transfer to a
registry).

### Command layer (`cmd/`)

Commands parse flags and arguments, validate them in `PreRunE`, then delegate:

- `cmd/pull.go` — `pull CHART [flags]`; builds `pull.Options` and calls `pull.Run()`.
- `cmd/push.go` — `push REGISTRY [flags]`; passes `args[0]` to `push.PushImages()`.
- `cmd/add.go` — `add IMAGE... [flags]`; builds `add.Options` and calls `add.Run()`.
- `cmd/root.go` — wires commands and provides `commandLogger(verbose bool) *slog.Logger`,
  a stderr logger at Debug level for `--verbose` and Info otherwise. There is no
  central Config/DI struct.

Keep commands thin: parse and validate, then delegate. Workflow logic belongs in
`internal/`.

### Internal packages (`internal/`)

- `pull/` — renders charts with the Helm SDK, resolves remote versions from
  `index.yaml`, extracts images, and stages archives plus `push_images.json`.
  `runner.go` exposes `Run`, `Runner`, `NewRunner`, `Options`, and `PullResult`.
- `add/` — augments an existing pull bundle. It reads `push_images.json`, deduplicates
  images, archives additions, and rewrites the manifest. It errors when no manifest
  exists because it is an augmentation workflow.
- `push/` — archives images into an OCI layout, probes and pushes them with bounded
  concurrency, reports progress, and provides helper-binary copy functions.
- `pushspec/` — owns the shared `push_images.json` and OCI-layout contract, including
  `ArchiveSpec`, `PushManifest`, `BuildSpecs`, and reference derivation.
- `chartimages/` — extracts image references from rendered manifests and annotations.
- `validation/` — reusable validators for flags and inputs.

Package dependencies point inward without cycles:

```text
pull  -> push, pushspec
add   -> push, pushspec
push  -> pushspec
pushspec -> stdlib, go-containerregistry/name
```

`pull.Run` accepts a status `io.Writer`, as do `push.ArchiveImages` and
`push.PushImages`; retain this human-readable progress seam.

### Test seams

`pull.Runner` and `add.Runner` deliberately keep function-field collaborators.
Tests replace these fields to run workflows without Helm, registry, or network
dependencies. Preserve the fields as seams; replace them only when the workflow
contract itself changes.

`push.Engine` owns the function-field collaborators for OCI layout, registry, and
interactive transfer operations. Tests create a fresh `push.NewEngine()` and
replace fields on that instance; do not reintroduce package-level mutable seams.

## Implementation rules

### Commands and validation

- Use `cobra.ExactArgs(N)` for required positional arguments.
- Use Cobra's `MarkFlagRequired()` for required flags; it owns presence checks.
- Put format and constraint checks in `PreRunE`, using `internal/validation`.
- Delegate format rules to the underlying library where one exists:
  - chart names: Helm `chartutil.ValidateMetadataName()`;
  - release names: Helm `chartutil.ValidateReleaseName()`;
  - namespaces: Kubernetes `validation.IsDNS1123Subdomain()`;
  - URLs: Go `url.Parse()` through `ValidateURL`, restricted to HTTP(S) for
    `index.yaml` repositories.
- Keep presence validation separate from format validation.

### Internal packages

- Keep the `cmd`/`internal` split and the `pull`/`push`/`pushspec` boundaries.
- Prefer existing helpers and focused package responsibilities.
- Keep packages independently testable; isolate mutable process state behind an
  injected collaborator when an external SDK requires it.
- Return clear wrapped errors, for example `fmt.Errorf("load chart: %w", err)`.
- Keep secrets out of logs.

### Logging and imports

- Use `commandLogger(verbose)` for the CLI's small amount of structured logging.
- Keep command-boundary logging in `cmd/`; use status writers for human-readable
  workflow progress.
- Use natural import names. Alias only for an invalid or awkward package name, a
  genuine name collision, or a package name that is too generic in the file.

## Testing and verification

- Add or update focused unit tests beside behavior changes, especially for manifests,
  archives, registry interactions, validation, and rendering.
- Keep unit tests in-package with the `*_test.go` suffix.
- Prefer table-driven tests when they cover multiple cases without obscuring intent.
- Format Go changes with `gofmt`.

### Static analysis

Treat `go vet` and `golangci-lint` as one verification pair. Whenever `go vet` is
run, run the matching linter command too:

```sh
go vet ./...
golangci-lint run ./...
```

Report an unavailable linter as a verification blocker rather than silently
substituting another check. Keep CI and local verification aligned with this pair.

### Command-layer tests

Helpers in `cmd/shared_test.go` provide:

- `ExecuteCommand(cmd, args)` — executes through `rootCmd` and captures output and
  errors.
- `AssertFlagExists`, `AssertFlagNotExists`, `AssertFlagType`, and
  `AssertFlagDefault` — verify flag metadata.
- `spyPullRun(retErr)` and `spyPushRun(retErr)` — replace workflow seams and capture
  mapped options. Each resets global command variables and returns a restore function;
  defer that restore function.
- `combinedErrorText(output)` — combines output and error text for attribution checks.

Test command behavior at the appropriate layer:

- flag metadata through the `Assert*` helpers;
- argument/flag mapping through the real `RunE` with a workflow spy;
- validation wiring by asserting an error mentions the responsible argument or flag.

`pflag` retains values across executions of the shared `rootCmd`; reset state with
`resetCmdVars()` whenever a test mutates flags.

```go
output := ExecuteCommand(pullCmd, []string{"nginx", "--concurrency", "8"})
output := ExecuteCommand(pushCmd, []string{"docker.io", "--concurrency", "4"})
```

### Standard commands

```sh
go test ./...
go build ./...
go vet ./...
golangci-lint run ./...
go run . --help
go run . pull nginx --verbose
go run . add busybox:1.36 -o ./out
go test ./... -run TestName
```

A change is ready when its tests and documentation reflect the behavior, Go files
are formatted, and the full test, build, vet, and golangci-lint checks pass—or any
environmental blocker is recorded explicitly.

## On-disk and runtime contracts

- `pull` loads charts in-process through Helm, supports local, HTTP(S), configured
  repository, and `oci://` sources, and writes archives plus `push_images.json`.
- `push` reads `push_images.json` from `--input-dir`, defaulting to the helper's
  directory when no input directory is provided.
- Pull bundles contain the OCI layout, `push_images.json`, the chart archive, and a
  staged `push_images` helper that runs as `./push_images REGISTRY`.
- `internal/pushspec/` owns the shared manifest and OCI-layout names.
- Temporary output goes to the current working directory unless an output directory
  is supplied.
- `e2e_registry_test.go` covers the registry push path end to end.
