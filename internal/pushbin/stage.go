package pushbin

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"helm-deep-pack/internal/upgrade"
	"helm-deep-pack/internal/validation"
)

var (
	lookupRelease          = upgrade.LookupRelease
	fetchReleaseAssetBytes = upgrade.FetchReleaseAssetBytes
	parseChecksumsText     = upgrade.ParseChecksumsText
	buildLocalHelperBinary = buildLocalHelperBinaryFromSource
)

func Stage(outputDir string) (string, error) {
	return StageForPlatform(context.Background(), outputDir, "", "")
}

func StageForPlatform(ctx context.Context, outputDir, destinationPlatform, currentVersion string) (string, error) {
	goos, goarch, err := resolveDestinationPlatform(destinationPlatform)
	if err != nil {
		return "", err
	}

	targetVersion := ""
	trimmedVersion := strings.TrimSpace(currentVersion)
	if trimmedVersion != "" && !strings.EqualFold(trimmedVersion, "dev") {
		targetVersion = trimmedVersion
	}
	var localBuildErr error
	if targetVersion == "" {
		path, err := buildLocalHelperBinary(ctx, outputDir, goos, goarch)
		if err == nil {
			return path, nil
		}
		localBuildErr = err
	}

	lookupOpts := upgrade.DefaultReleaseLookupOptions(currentVersion)
	releaseInfo, err := lookupRelease(ctx, lookupOpts, targetVersion)
	if err != nil {
		if localBuildErr != nil {
			return "", fmt.Errorf("build local push helper for dev run failed: %v; resolve helper release metadata: %w", localBuildErr, err)
		}
		return "", fmt.Errorf("resolve helper release metadata: %w", err)
	}
	helperAssetName := helperArchiveName(releaseInfo.BareVersion, goos, goarch)
	helperAsset, ok := findAsset(releaseInfo.Assets, helperAssetName)
	if !ok {
		return "", fmt.Errorf("release %s does not include %q required for destination platform %s/%s. This usually means your helm-deep-pack version predates helper release assets; upgrade helm-deep-pack and retry (available assets: %s)", releaseInfo.TagName, helperAssetName, goos, goarch, strings.Join(assetNames(releaseInfo.Assets), ", "))
	}
	checksumAsset, ok := findAsset(releaseInfo.Assets, "checksums.txt")
	if !ok {
		return "", fmt.Errorf("release %s is missing checksums.txt", releaseInfo.TagName)
	}
	releaseLabel := "release " + releaseInfo.TagName

	checksumBody, err := fetchReleaseAssetBytes(ctx, lookupOpts, checksumAsset)
	if err != nil {
		return "", fmt.Errorf("download checksums from %s: %w", releaseLabel, err)
	}
	checksums, err := parseChecksumsText(string(checksumBody))
	if err != nil {
		return "", fmt.Errorf("parse checksums: %w", err)
	}
	expectedHash, ok := checksums[helperAssetName]
	if !ok {
		return "", fmt.Errorf("checksums.txt does not include %q", helperAssetName)
	}

	helperArchive, err := fetchReleaseAssetBytes(ctx, lookupOpts, helperAsset)
	if err != nil {
		return "", fmt.Errorf("download helper asset %q from %s: %w", helperAssetName, releaseLabel, err)
	}
	if helperAsset.Size > 0 && int64(len(helperArchive)) != helperAsset.Size {
		return "", fmt.Errorf("downloaded helper asset %q size mismatch: got %d bytes, expected %d", helperAssetName, len(helperArchive), helperAsset.Size)
	}
	sum := sha256.Sum256(helperArchive)
	actualHash := hex.EncodeToString(sum[:])
	if !strings.EqualFold(actualHash, expectedHash) {
		return "", fmt.Errorf("checksum mismatch for %s: expected %s, got %s", helperAssetName, expectedHash, actualHash)
	}

	bin, err := extractHelperBinary(helperArchive, helperAssetName, goos)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return "", fmt.Errorf("create output dir: %w", err)
	}
	dst := filepath.Join(outputDir, helperBinaryName(goos))
	if err := os.WriteFile(dst, bin, 0o755); err != nil {
		return "", fmt.Errorf("write helper binary: %w", err)
	}
	if goos != "windows" {
		if err := os.Chmod(dst, 0o755); err != nil {
			return "", fmt.Errorf("chmod helper binary: %w", err)
		}
	}
	return dst, nil
}

func resolveDestinationPlatform(destinationPlatform string) (string, string, error) {
	if strings.TrimSpace(destinationPlatform) == "" {
		return runtime.GOOS, runtime.GOARCH, nil
	}
	goos, goarch, err := validation.ParseDestinationPlatform(destinationPlatform)
	if err != nil {
		return "", "", fmt.Errorf("parse destination platform: %w", err)
	}
	if err := validation.ValidateDestinationPlatform("--destination-platform", goos+"/"+goarch); err != nil {
		return "", "", err
	}
	return goos, goarch, nil
}

func helperArchiveName(bareVersion, goos, goarch string) string {
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("push_images_%s_%s_%s.%s", bareVersion, goos, goarch, ext)
}

func helperBinaryName(goos string) string {
	if goos == "windows" {
		return "push_images.exe"
	}
	return "push_images"
}

func buildLocalHelperBinaryFromSource(ctx context.Context, outputDir, goos, goarch string) (string, error) {
	moduleRoot, err := findModuleRoot()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return "", fmt.Errorf("create output dir: %w", err)
	}
	dst := filepath.Join(outputDir, helperBinaryName(goos))
	cmd := exec.CommandContext(ctx, "go", "build", "-trimpath", "-ldflags=-s -w", "-o", dst, "./cmd/pushimages")
	cmd.Dir = moduleRoot
	cmd.Env = append(os.Environ(),
		"CGO_ENABLED=0",
		"GOOS="+goos,
		"GOARCH="+goarch,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("go build ./cmd/pushimages for %s/%s: %w (%s)", goos, goarch, err, strings.TrimSpace(string(out)))
	}
	if goos != "windows" {
		if err := os.Chmod(dst, 0o755); err != nil {
			return "", fmt.Errorf("chmod built helper binary: %w", err)
		}
	}
	return dst, nil
}

func findModuleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve working directory: %w", err)
	}
	for {
		candidate := filepath.Join(dir, "go.mod")
		data, readErr := os.ReadFile(candidate)
		if readErr == nil && bytes.Contains(data, []byte("module helm-deep-pack")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("could not locate module root for local build (run from the helm-deep-pack repository or use a released binary)")
}

func findAsset(assets []upgrade.ReleaseAsset, name string) (upgrade.ReleaseAsset, bool) {
	for _, asset := range assets {
		if asset.Name == name {
			return asset, true
		}
	}
	return upgrade.ReleaseAsset{}, false
}

func assetNames(assets []upgrade.ReleaseAsset) []string {
	names := make([]string, 0, len(assets))
	for _, asset := range assets {
		names = append(names, asset.Name)
	}
	return names
}

func extractHelperBinary(archive []byte, archiveName, goos string) ([]byte, error) {
	switch {
	case strings.HasSuffix(strings.ToLower(archiveName), ".zip"):
		return extractFromZip(archive, goos)
	case strings.HasSuffix(strings.ToLower(archiveName), ".tar.gz"):
		return extractFromTarGz(archive, goos)
	default:
		return nil, fmt.Errorf("unsupported helper archive format: %q", archiveName)
	}
}

func extractFromTarGz(archive []byte, goos string) ([]byte, error) {
	gzReader, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("open helper gzip stream: %w", err)
	}
	defer func() {
		_ = gzReader.Close()
	}()
	tr := tar.NewReader(gzReader)
	expected := helperBinaryName(goos)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read helper tar archive: %w", err)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		if filepath.Base(header.Name) != expected {
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("read helper tar entry: %w", err)
		}
		return data, nil
	}
	return nil, fmt.Errorf("helper binary %q not found in archive", expected)
}

func extractFromZip(archive []byte, goos string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, fmt.Errorf("open helper zip archive: %w", err)
	}
	expected := helperBinaryName(goos)
	for _, file := range zr.File {
		if filepath.Base(file.Name) != expected {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			return nil, fmt.Errorf("open helper zip entry %q: %w", file.Name, err)
		}
		data, readErr := io.ReadAll(rc)
		closeErr := rc.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read helper zip entry %q: %w", file.Name, readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close helper zip entry %q: %w", file.Name, closeErr)
		}
		return data, nil
	}
	return nil, fmt.Errorf("helper binary %q not found in archive", expected)
}
