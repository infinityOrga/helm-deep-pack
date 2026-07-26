# Publishing helm-deep-pack

This project publishes GitHub Releases from Git tags. Pushing a `v*` tag triggers
the `release` workflow, which runs vet/test/build and then publishes binaries via
GoReleaser.

## Manual steps

1. Ensure your branch is clean and up to date:

```bash
git checkout main
git pull --ff-only
git status --short
```

2. Pick the next version and create an annotated tag:

```bash
git tag -a v1.2.3 -m "Release v1.2.3"
```

3. Push the tag:

```bash
git push origin v1.2.3
```

4. Monitor the release workflow in GitHub Actions:

```bash
echo "https://github.com/infinityOrga/helm-deep-pack/actions/workflows/release.yml"
```

5. (Optional) Edit generated release notes in the GitHub Release UI after publish.

## Optional local dry run before tagging

```bash
go run github.com/goreleaser/goreleaser/v2@latest check
go run github.com/goreleaser/goreleaser/v2@latest release --snapshot --clean
```

This builds artifacts into `dist/` without creating a GitHub Release.

## Re-do a mistaken tag

```bash
git push origin :refs/tags/v1.2.3
git tag -d v1.2.3
```

## Manual E2E test (not part of release pipeline)

The registry E2E suite requires local infrastructure and network access.

```bash
E2E_REGISTRY_TEST=1 go test ./... -run TestRegistry
```
