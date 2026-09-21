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
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"helm-deep-pack/internal/upgrade"
)

func TestStageForPlatform_DefaultsToHostPlatform(t *testing.T) {
	restore := stubReleaseFetcher(t, runtime.GOOS, runtime.GOARCH, "helper-bytes")
	defer restore()

	outDir := t.TempDir()
	path, err := StageForPlatform(context.Background(), outDir, "", "v1.2.3")
	if err != nil {
		t.Fatalf("StageForPlatform() error = %v", err)
	}
	wantName := "push_images"
	if runtime.GOOS == "windows" {
		wantName = "push_images.exe"
	}
	if filepath.Base(path) != wantName {
		t.Fatalf("staged path base = %q, want %q", filepath.Base(path), wantName)
	}
}

func TestStageForPlatform_ExplicitWindowsAmd64(t *testing.T) {
	restore := stubReleaseFetcher(t, "windows", "amd64", "win-helper")
	defer restore()

	outDir := t.TempDir()
	path, err := StageForPlatform(context.Background(), outDir, "windows/amd64", "1.2.3")
	if err != nil {
		t.Fatalf("StageForPlatform() error = %v", err)
	}
	if filepath.Base(path) != "push_images.exe" {
		t.Fatalf("staged path base = %q, want push_images.exe", filepath.Base(path))
	}
}

func TestStageForPlatform_FailsOnChecksumMismatch(t *testing.T) {
	originalLookup := lookupRelease
	originalFetch := fetchReleaseAssetBytes
	originalParse := parseChecksumsText
	defer func() {
		lookupRelease = originalLookup
		fetchReleaseAssetBytes = originalFetch
		parseChecksumsText = originalParse
	}()

	assetName := "push_images_1.2.3_linux_amd64.tar.gz"
	archiveData := buildTarGz(t, "push_images", []byte("helper"))
	lookupRelease = func(context.Context, upgrade.ReleaseLookupOptions, string) (upgrade.ReleaseMetadata, error) {
		return upgrade.ReleaseMetadata{
			TagName:     "v1.2.3",
			BareVersion: "1.2.3",
			Assets: []upgrade.ReleaseAsset{
				{Name: assetName, URL: "https://example.com/helper", Size: int64(len(archiveData))},
				{Name: "checksums.txt", URL: "https://example.com/checksums", Size: 20},
			},
		}, nil
	}
	fetchReleaseAssetBytes = func(_ context.Context, _ upgrade.ReleaseLookupOptions, asset upgrade.ReleaseAsset) ([]byte, error) {
		switch asset.Name {
		case assetName:
			return archiveData, nil
		case "checksums.txt":
			return []byte("deadbeef  " + assetName + "\n"), nil
		default:
			return nil, fmt.Errorf("unexpected asset %q", asset.Name)
		}
	}
	parseChecksumsText = upgrade.ParseChecksumsText

	_, err := StageForPlatform(context.Background(), t.TempDir(), "linux/amd64", "1.2.3")
	if err == nil {
		t.Fatal("expected checksum mismatch error, got nil")
	}
}

func TestStageForPlatform_MissingHelperAssetErrorIsActionable(t *testing.T) {
	originalLookup := lookupRelease
	defer func() {
		lookupRelease = originalLookup
	}()

	lookupRelease = func(context.Context, upgrade.ReleaseLookupOptions, string) (upgrade.ReleaseMetadata, error) {
		return upgrade.ReleaseMetadata{
			TagName:     "v0.1.4",
			BareVersion: "0.1.4",
			Assets: []upgrade.ReleaseAsset{
				{Name: "helm-deep-pack_0.1.4_linux_amd64.tar.gz", URL: "https://example.com/cli"},
			},
		}, nil
	}

	_, err := StageForPlatform(context.Background(), t.TempDir(), "windows/amd64", "0.1.4")
	if err == nil {
		t.Fatal("expected missing helper asset error")
	}
	if !strings.Contains(err.Error(), "predates helper release assets") {
		t.Fatalf("expected actionable message, got: %v", err)
	}
}

func TestStageForPlatform_DevBuildsHelperLocally(t *testing.T) {
	originalLookup := lookupRelease
	originalBuild := buildLocalHelperBinary
	defer func() {
		lookupRelease = originalLookup
		buildLocalHelperBinary = originalBuild
	}()

	lookupRelease = func(context.Context, upgrade.ReleaseLookupOptions, string) (upgrade.ReleaseMetadata, error) {
		return upgrade.ReleaseMetadata{}, fmt.Errorf("lookup should not be called for dev local build")
	}
	buildLocalHelperBinary = func(_ context.Context, outputDir, goos, goarch string) (string, error) {
		return filepath.Join(outputDir, "push_images"), nil
	}

	outDir := t.TempDir()
	path, err := StageForPlatform(context.Background(), outDir, "linux/amd64", "dev")
	if err != nil {
		t.Fatalf("StageForPlatform() error = %v", err)
	}
	if path != filepath.Join(outDir, "push_images") {
		t.Fatalf("unexpected staged path: %q", path)
	}
}

func TestLocalHelperBuildEnvironment_UsesCgoTargetToolchain(t *testing.T) {
	env := localHelperBuildEnvironment([]string{
		"CGO_ENABLED=host-default",
		"CC=host-gcc",
		"CXX=host-g++",
		"PATH=/usr/bin",
	}, "linux", "arm64")

	for key, want := range map[string]string{
		"CGO_ENABLED": "1",
		"GOOS":        "linux",
		"GOARCH":      "arm64",
		"CC":          "aarch64-linux-gnu-gcc",
		"CXX":         "aarch64-linux-gnu-g++",
		"PATH":        "/usr/bin",
	} {
		if got := environmentValue(env, key); got != want {
			t.Errorf("environment value %s = %q, want %q", key, got, want)
		}
	}
}

func TestLocalHelperCgoToolchain_CrossTargets(t *testing.T) {
	tests := map[string]struct {
		goos    string
		goarch  string
		wantCC  string
		wantCXX string
	}{
		"darwin amd64": {
			goos:    "darwin",
			goarch:  "amd64",
			wantCC:  "o64-clang",
			wantCXX: "o64-clang++",
		},
		"darwin arm64": {
			goos:    "darwin",
			goarch:  "arm64",
			wantCC:  "oa64-clang",
			wantCXX: "oa64-clang++",
		},
		"linux arm64": {
			goos:    "linux",
			goarch:  "arm64",
			wantCC:  "aarch64-linux-gnu-gcc",
			wantCXX: "aarch64-linux-gnu-g++",
		},
		"windows amd64": {
			goos:    "windows",
			goarch:  "amd64",
			wantCC:  "x86_64-w64-mingw32-gcc",
			wantCXX: "x86_64-w64-mingw32-g++",
		},
		"windows arm64": {
			goos:    "windows",
			goarch:  "arm64",
			wantCC:  "/llvm-mingw/bin/aarch64-w64-mingw32-gcc",
			wantCXX: "/llvm-mingw/bin/aarch64-w64-mingw32-g++",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if tt.goos == runtime.GOOS && tt.goarch == runtime.GOARCH {
				t.Skip("native targets use the host cgo toolchain")
			}
			gotCC, gotCXX := localHelperCgoToolchain(tt.goos, tt.goarch)
			if gotCC != tt.wantCC || gotCXX != tt.wantCXX {
				t.Fatalf("localHelperCgoToolchain() = %q, %q; want %q, %q", gotCC, gotCXX, tt.wantCC, tt.wantCXX)
			}
		})
	}
}

func TestStageForPlatform_DevFallsBackToReleaseWhenLocalBuildFails(t *testing.T) {
	restore := stubReleaseFetcher(t, "linux", "amd64", "release-helper")
	defer restore()

	originalBuild := buildLocalHelperBinary
	defer func() { buildLocalHelperBinary = originalBuild }()
	buildLocalHelperBinary = func(_ context.Context, _, _, _ string) (string, error) {
		return "", fmt.Errorf("local build failed")
	}

	outDir := t.TempDir()
	path, err := StageForPlatform(context.Background(), outDir, "linux/amd64", "dev")
	if err != nil {
		t.Fatalf("StageForPlatform() error = %v", err)
	}
	if filepath.Base(path) != "push_images" {
		t.Fatalf("unexpected staged helper name: %q", filepath.Base(path))
	}
}

func TestStageForPlatform_DevErrorIncludesLocalAndReleaseFailure(t *testing.T) {
	originalLookup := lookupRelease
	originalBuild := buildLocalHelperBinary
	defer func() {
		lookupRelease = originalLookup
		buildLocalHelperBinary = originalBuild
	}()
	buildLocalHelperBinary = func(_ context.Context, _, _, _ string) (string, error) {
		return "", fmt.Errorf("cwd has no module root")
	}
	lookupRelease = func(context.Context, upgrade.ReleaseLookupOptions, string) (upgrade.ReleaseMetadata, error) {
		return upgrade.ReleaseMetadata{}, fmt.Errorf("github offline")
	}

	_, err := StageForPlatform(context.Background(), t.TempDir(), "linux/amd64", "dev")
	if err == nil {
		t.Fatal("expected failure")
	}
	if !strings.Contains(err.Error(), "build local push helper for dev run failed") || !strings.Contains(err.Error(), "resolve helper release metadata") {
		t.Fatalf("expected combined error context, got: %v", err)
	}
}

func stubReleaseFetcher(t *testing.T, goos, goarch, payload string) func() {
	t.Helper()
	originalLookup := lookupRelease
	originalFetch := fetchReleaseAssetBytes
	originalParse := parseChecksumsText

	archiveName := "push_images_1.2.3_" + goos + "_" + goarch + ".tar.gz"
	entryName := "push_images"
	if goos == "windows" {
		archiveName = "push_images_1.2.3_" + goos + "_" + goarch + ".zip"
		entryName = "push_images.exe"
	}
	var archiveData []byte
	if goos == "windows" {
		archiveData = buildZip(t, entryName, []byte(payload))
	} else {
		archiveData = buildTarGz(t, entryName, []byte(payload))
	}
	sum := sha256.Sum256(archiveData)
	checksums := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), archiveName)

	lookupRelease = func(context.Context, upgrade.ReleaseLookupOptions, string) (upgrade.ReleaseMetadata, error) {
		return upgrade.ReleaseMetadata{
			TagName:     "v1.2.3",
			BareVersion: "1.2.3",
			Assets: []upgrade.ReleaseAsset{
				{Name: archiveName, URL: "https://example.com/helper", Size: int64(len(archiveData))},
				{Name: "checksums.txt", URL: "https://example.com/checksums", Size: int64(len(checksums))},
			},
		}, nil
	}
	fetchReleaseAssetBytes = func(_ context.Context, _ upgrade.ReleaseLookupOptions, asset upgrade.ReleaseAsset) ([]byte, error) {
		switch asset.Name {
		case archiveName:
			return archiveData, nil
		case "checksums.txt":
			return []byte(checksums), nil
		default:
			return nil, fmt.Errorf("unexpected asset %q", asset.Name)
		}
	}
	parseChecksumsText = upgrade.ParseChecksumsText

	return func() {
		lookupRelease = originalLookup
		fetchReleaseAssetBytes = originalFetch
		parseChecksumsText = originalParse
	}
}

func environmentValue(env []string, key string) string {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}
	return ""
}

func buildTarGz(t *testing.T, name string, payload []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(payload)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatalf("WriteHeader() error = %v", err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("Close tar writer error = %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("Close gzip writer error = %v", err)
	}
	return out.Bytes()
}

func buildZip(t *testing.T, name string, payload []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := io.Copy(w, bytes.NewReader(payload)); err != nil {
		t.Fatalf("Write zip payload error = %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("Close zip writer error = %v", err)
	}
	return out.Bytes()
}
