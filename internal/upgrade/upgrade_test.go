package upgrade

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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeTag(t *testing.T) {
	tag, bare, err := normalizeTag("v1.2.3")
	if err != nil {
		t.Fatalf("normalizeTag() error = %v", err)
	}
	if tag != "v1.2.3" || bare != "1.2.3" {
		t.Fatalf("normalizeTag() = (%q, %q), want (%q, %q)", tag, bare, "v1.2.3", "1.2.3")
	}
}

func TestAssetName(t *testing.T) {
	if got := assetName("1.2.3", "linux", "amd64"); got != "helm-deep-pack_1.2.3_linux_amd64.tar.gz" {
		t.Fatalf("assetName() = %q", got)
	}
	if got := assetName("1.2.3", "windows", "amd64"); got != "helm-deep-pack_1.2.3_windows_amd64.zip" {
		t.Fatalf("assetName() windows = %q", got)
	}
}

func TestParseChecksums(t *testing.T) {
	m, err := parseChecksums("abc123  file1\nfff999  file2\n")
	if err != nil {
		t.Fatalf("parseChecksums() error = %v", err)
	}
	if m["file1"] != "abc123" || m["file2"] != "fff999" {
		t.Fatalf("parseChecksums() = %#v", m)
	}
}

func TestDefaultReleaseLookupOptions_UsesConfiguredDefaultsAndCurrentVersion(t *testing.T) {
	origReadGitRemoteOrigin := readGitRemoteOrigin
	origBaseURL := releaseBaseURL
	defer func() {
		readGitRemoteOrigin = origReadGitRemoteOrigin
		releaseBaseURL = origBaseURL
	}()

	readGitRemoteOrigin = func() (string, error) {
		return "git@github.com:acme-org/mirror-tool.git", nil
	}
	releaseBaseURL = "https://gh.example.test/api/v3"

	opts := DefaultReleaseLookupOptions("dev")
	if opts.Owner != "acme-org" {
		t.Fatalf("owner = %q, want %q", opts.Owner, "acme-org")
	}
	if opts.Repo != "mirror-tool" {
		t.Fatalf("repo = %q, want %q", opts.Repo, "mirror-tool")
	}
	if opts.BaseURL != "https://gh.example.test/api/v3" {
		t.Fatalf("baseURL = %q, want %q", opts.BaseURL, "https://gh.example.test/api/v3")
	}
	if opts.CurrentVersion != "dev" {
		t.Fatalf("currentVersion = %q, want %q", opts.CurrentVersion, "dev")
	}
}

func TestDefaultReleaseTarget_UsesBuiltInDefaultsOutsideDev(t *testing.T) {
	origReadGitRemoteOrigin := readGitRemoteOrigin
	defer func() { readGitRemoteOrigin = origReadGitRemoteOrigin }()

	readGitRemoteOrigin = func() (string, error) {
		return "git@github.com:acme-org/mirror-tool.git", nil
	}

	owner, repo := defaultReleaseTarget("v1.2.3")
	if owner != "infinityOrga" {
		t.Fatalf("owner = %q, want %q", owner, "infinityOrga")
	}
	if repo != "helm-deep-pack" {
		t.Fatalf("repo = %q, want %q", repo, "helm-deep-pack")
	}
}

func TestParseGitRemote(t *testing.T) {
	tests := []struct {
		name       string
		remote     string
		wantOwner  string
		wantRepo   string
		wantParsed bool
	}{
		{name: "https", remote: "https://github.com/acme-org/mirror-tool.git", wantOwner: "acme-org", wantRepo: "mirror-tool", wantParsed: true},
		{name: "ssh", remote: "git@github.com:acme-org/mirror-tool.git", wantOwner: "acme-org", wantRepo: "mirror-tool", wantParsed: true},
		{name: "ssh_url", remote: "ssh://git@github.com/acme-org/mirror-tool.git", wantOwner: "acme-org", wantRepo: "mirror-tool", wantParsed: true},
		{name: "invalid", remote: "not-a-remote", wantParsed: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner, repo, ok := parseGitRemote(tt.remote)
			if ok != tt.wantParsed {
				t.Fatalf("parseGitRemote(%q) parsed = %v, want %v", tt.remote, ok, tt.wantParsed)
			}
			if !tt.wantParsed {
				return
			}
			if owner != tt.wantOwner {
				t.Fatalf("owner = %q, want %q", owner, tt.wantOwner)
			}
			if repo != tt.wantRepo {
				t.Fatalf("repo = %q, want %q", repo, tt.wantRepo)
			}
		})
	}
}

func TestExtractFromTarGz(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "test.tar.gz")
	if err := os.WriteFile(archive, buildTarGz(t, "bin/helm-deep-pack", []byte("new-binary")), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	var out bytes.Buffer
	if err := extractBinary(archive, "linux", &out); err != nil {
		t.Fatalf("extractBinary() error = %v", err)
	}
	if out.String() != "new-binary" {
		t.Fatalf("extractBinary() output = %q", out.String())
	}
}

func TestExtractFromZip(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "test.zip")
	if err := os.WriteFile(archive, buildZip(t, "bin/helm-deep-pack.exe", []byte("new-binary")), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	var out bytes.Buffer
	if err := extractBinary(archive, "windows", &out); err != nil {
		t.Fatalf("extractBinary() error = %v", err)
	}
	if out.String() != "new-binary" {
		t.Fatalf("extractBinary() output = %q", out.String())
	}
}

func TestReplaceExecutable(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "helm-deep-pack")
	newFile := filepath.Join(dir, "helm-deep-pack.new")
	if err := os.WriteFile(current, []byte("old"), 0o755); err != nil {
		t.Fatalf("WriteFile current error = %v", err)
	}
	if err := os.WriteFile(newFile, []byte("new"), 0o755); err != nil {
		t.Fatalf("WriteFile new error = %v", err)
	}

	scheduled, err := replaceExecutable(current, newFile)
	if err != nil {
		t.Fatalf("replaceExecutable() error = %v", err)
	}
	if scheduled {
		t.Fatalf("replaceExecutable() scheduled update on non-windows test path")
	}
	content, err := os.ReadFile(current)
	if err != nil {
		t.Fatalf("ReadFile current error = %v", err)
	}
	if string(content) != "new" {
		t.Fatalf("current content = %q", string(content))
	}
}

func TestCreateWindowsHelperCopy(t *testing.T) {
	oldResolve := resolveExecutablePath
	oldEval := evalSymlinks
	defer func() {
		resolveExecutablePath = oldResolve
		evalSymlinks = oldEval
	}()

	dir := t.TempDir()
	src := filepath.Join(dir, "source.exe")
	want := []byte("helper-bytes")
	if err := os.WriteFile(src, want, 0o755); err != nil {
		t.Fatalf("WriteFile source error = %v", err)
	}
	resolveExecutablePath = func() (string, error) { return src, nil }
	evalSymlinks = func(path string) (string, error) { return path, nil }

	copyPath, err := createWindowsHelperCopy()
	if err != nil {
		t.Fatalf("createWindowsHelperCopy() error = %v", err)
	}
	defer func() { _ = os.RemoveAll(filepath.Dir(copyPath)) }()

	got, err := os.ReadFile(copyPath)
	if err != nil {
		t.Fatalf("ReadFile helper copy error = %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("helper copy contents = %q", string(got))
	}
}

func TestRunUpgradesBinary(t *testing.T) {
	oldResolve := resolveExecutablePath
	oldEval := evalSymlinks
	oldOS := runtimeGOOS
	oldArch := runtimeGOARCH
	defer func() {
		resolveExecutablePath = oldResolve
		evalSymlinks = oldEval
		runtimeGOOS = oldOS
		runtimeGOARCH = oldArch
	}()
	runtimeGOOS = "linux"
	runtimeGOARCH = "amd64"

	dir := t.TempDir()
	exe := filepath.Join(dir, "helm-deep-pack")
	if err := os.WriteFile(exe, []byte("old-binary"), 0o755); err != nil {
		t.Fatalf("WriteFile executable error = %v", err)
	}
	resolveExecutablePath = func() (string, error) { return exe, nil }
	evalSymlinks = func(path string) (string, error) { return path, nil }

	archiveName := "helm-deep-pack_1.2.3_linux_amd64.tar.gz"
	archiveBody := buildTarGz(t, "helm-deep-pack", []byte("new-binary"))
	sum := sha256.Sum256(archiveBody)
	checksumBody := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), archiveName)

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, fmt.Sprintf(`{"tag_name":"v1.2.3","assets":[{"name":"%s","browser_download_url":"%s/archive","size":%d},{"name":"checksums.txt","browser_download_url":"%s/checksums","size":%d}]}`,
			archiveName, serverURL(r), len(archiveBody), serverURL(r), len(checksumBody)))
	})
	mux.HandleFunc("/archive", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archiveBody)
	})
	mux.HandleFunc("/checksums", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, checksumBody)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	var out bytes.Buffer
	err := Run(context.Background(), Options{
		CurrentVersion: "1.0.0",
		AssumeYes:      true,
		Owner:          "o",
		Repo:           "r",
		BaseURL:        server.URL,
		Out:            &out,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	content, err := os.ReadFile(exe)
	if err != nil {
		t.Fatalf("ReadFile executable error = %v", err)
	}
	if string(content) != "new-binary" {
		t.Fatalf("upgraded executable content = %q", string(content))
	}
	if !strings.Contains(out.String(), "upgraded helm-deep-pack") {
		t.Fatalf("Run() output = %q", out.String())
	}
}

func TestRunRejectsChecksumMismatch(t *testing.T) {
	oldResolve := resolveExecutablePath
	oldEval := evalSymlinks
	oldOS := runtimeGOOS
	oldArch := runtimeGOARCH
	defer func() {
		resolveExecutablePath = oldResolve
		evalSymlinks = oldEval
		runtimeGOOS = oldOS
		runtimeGOARCH = oldArch
	}()
	runtimeGOOS = "linux"
	runtimeGOARCH = "amd64"

	dir := t.TempDir()
	exe := filepath.Join(dir, "helm-deep-pack")
	if err := os.WriteFile(exe, []byte("old-binary"), 0o755); err != nil {
		t.Fatalf("WriteFile executable error = %v", err)
	}
	resolveExecutablePath = func() (string, error) { return exe, nil }
	evalSymlinks = func(path string) (string, error) { return path, nil }

	archiveName := "helm-deep-pack_1.2.3_linux_amd64.tar.gz"
	archiveBody := buildTarGz(t, "helm-deep-pack", []byte("new-binary"))

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, fmt.Sprintf(`{"tag_name":"v1.2.3","assets":[{"name":"%s","browser_download_url":"%s/archive","size":%d},{"name":"checksums.txt","browser_download_url":"%s/checksums","size":20}]}`,
			archiveName, serverURL(r), len(archiveBody), serverURL(r)))
	})
	mux.HandleFunc("/archive", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archiveBody)
	})
	mux.HandleFunc("/checksums", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "deadbeef  "+archiveName+"\n")
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	err := Run(context.Background(), Options{
		CurrentVersion: "1.0.0",
		AssumeYes:      true,
		Owner:          "o",
		Repo:           "r",
		BaseURL:        server.URL,
		Out:            io.Discard,
	})
	if err == nil {
		t.Fatal("expected checksum mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("error = %v", err)
	}
}

func buildTarGz(t *testing.T, name string, payload []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	gw := gzip.NewWriter(&out)
	tw := tar.NewWriter(gw)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(payload)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatalf("WriteHeader error = %v", err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatalf("Write error = %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("Close tar writer error = %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("Close gzip writer error = %v", err)
	}
	return out.Bytes()
}

func buildZip(t *testing.T, name string, payload []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	f, err := zw.Create(name)
	if err != nil {
		t.Fatalf("Create zip entry error = %v", err)
	}
	if _, err := f.Write(payload); err != nil {
		t.Fatalf("Write zip entry error = %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("Close zip writer error = %v", err)
	}
	return out.Bytes()
}

func serverURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
