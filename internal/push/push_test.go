package push

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"helm-deep-pack/internal/pushspec"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func withRegistryProbeClient(t *testing.T, fn func(*http.Request) (*http.Response, error)) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripperFunc(fn)}
}

func registryProbeResponse(status int, contentType, body string) *http.Response {
	headers := make(http.Header)
	if contentType != "" {
		headers.Set("Content-Type", contentType)
	}
	headers.Set("Docker-Distribution-Api-Version", "registry/2.0")

	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     headers,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// websiteProbeResponse builds a /v2/ probe response that lacks any Docker
// Registry v2 signal, simulating a generic website rather than a registry.
func websiteProbeResponse(status int, contentType, body string) *http.Response {
	headers := make(http.Header)
	if contentType != "" {
		headers.Set("Content-Type", contentType)
	}

	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     headers,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func newPushTestEngine(client *http.Client) Engine {
	engine := NewEngine()
	engine.probeClient = client
	return engine
}

func pushImagesForTest(t *testing.T, client *http.Client, opts Options, status ...io.Writer) error {
	t.Helper()
	return pushImagesWithEngineForTest(t, newPushTestEngine(client), opts, status...)
}

func pushImagesWithEngineForTest(t *testing.T, engine Engine, opts Options, status ...io.Writer) error {
	t.Helper()
	return engine.pushImages(context.Background(), opts, engine.probeClient, status...)
}

func TestPushImagesUsesManifestDigests(t *testing.T) {
	probeClient := withRegistryProbeClient(t, func(*http.Request) (*http.Response, error) {
		return registryProbeResponse(http.StatusOK, "application/json", ""), nil
	})
	engine := newPushTestEngine(probeClient)
	var callsMu sync.Mutex
	var calls []string
	engine.copyImageToRegistry = func(_ context.Context, registry string, _ bool, _ layout.Path, sourceImage, target, ociDigest string) error {
		callsMu.Lock()
		defer callsMu.Unlock()
		calls = append(calls, registry+"|"+sourceImage+"|"+target+"|"+ociDigest)
		return nil
	}
	engine.loadOCILayout = func(path string) (layout.Path, error) {
		return layout.Path(path), nil
	}
	engine.resolveExecutablePath = func() (string, error) {
		return "/unused/helper", nil
	}

	dir := t.TempDir()
	manifest, err := pushspec.GeneratePushManifest([]pushspec.ArchiveSpec{
		{
			Image:     "quay.io/example/api:v1",
			Target:    "example/api:v1",
			OCIDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			Image:     "busybox:1.36",
			Target:    "library/busybox:1.36",
			OCIDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		},
	})
	if err != nil {
		t.Fatalf("pushspec.GeneratePushManifest() error = %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, pushspec.PushManifestFileName()), []byte(manifest), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if err := pushImagesWithEngineForTest(t, engine, Options{Registry: "registry.local:5000", InputDir: dir, Concurrency: 4, All: true}); err != nil {
		t.Fatalf("PushImages() error = %v", err)
	}

	if len(calls) != 2 {
		t.Fatalf("PushImages() calls = %d, want 2", len(calls))
	}
	want := map[string]struct{}{
		"registry.local:5000|quay.io/example/api:v1|example/api:v1|sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": {},
		"registry.local:5000|busybox:1.36|library/busybox:1.36|sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb":     {},
	}
	for _, call := range calls {
		if _, ok := want[call]; !ok {
			t.Fatalf("PushImages() unexpected call = %q", call)
		}
		delete(want, call)
	}
	if len(want) != 0 {
		t.Fatalf("PushImages() missing calls: %v", want)
	}
}

func TestPushImagesRejectsInvalidConcurrency(t *testing.T) {
	opts := Options{
		Registry:    "docker.io",
		Concurrency: 0,
	}

	err := pushImagesForTest(t, nil, opts)
	if err == nil {
		t.Fatal("PushImages() error = nil, want concurrency validation error")
	}
	if !strings.Contains(err.Error(), "concurrency") {
		t.Fatalf("PushImages() error = %v, want concurrency-related error", err)
	}
}

func TestPushImagesUsesRegistryNamespacePathAsDestinationPrefix(t *testing.T) {
	probeClient := withRegistryProbeClient(t, func(*http.Request) (*http.Response, error) {
		return registryProbeResponse(http.StatusOK, "application/json", ""), nil
	})
	engine := newPushTestEngine(probeClient)
	var calledRegistry string
	engine.copyImageToRegistry = func(_ context.Context, registry string, _ bool, _ layout.Path, _, _, _ string) error {
		calledRegistry = registry
		return nil
	}
	engine.loadOCILayout = func(path string) (layout.Path, error) {
		return layout.Path(path), nil
	}
	engine.resolveExecutablePath = func() (string, error) {
		return "/unused/helper", nil
	}

	dir := t.TempDir()
	manifest, err := pushspec.GeneratePushManifest([]pushspec.ArchiveSpec{{
		Image:     "busybox:1.36",
		Target:    "library/busybox:1.36",
		OCIDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	}})
	if err != nil {
		t.Fatalf("pushspec.GeneratePushManifest() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, pushspec.PushManifestFileName()), []byte(manifest), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if err := pushImagesWithEngineForTest(t, engine, Options{Registry: "registry.local:5000/team/sub", InputDir: dir, Concurrency: 1, All: true}); err != nil {
		t.Fatalf("PushImages() error = %v", err)
	}
	if calledRegistry != "registry.local:5000/team/sub" {
		t.Fatalf("engine.copyImageToRegistry registry = %q, want %q", calledRegistry, "registry.local:5000/team/sub")
	}
}

func TestPushImagesResolvesDefaultInputDir(t *testing.T) {
	probeClient := withRegistryProbeClient(t, func(*http.Request) (*http.Response, error) {
		return registryProbeResponse(http.StatusOK, "application/json", ""), nil
	})
	engine := newPushTestEngine(probeClient)
	engine.copyImageToRegistry = func(_ context.Context, _ string, _ bool, _ layout.Path, _, _, _ string) error {
		return nil
	}

	baseDir := t.TempDir()
	t.Chdir(baseDir)
	helperDir := filepath.Join(baseDir, "helper")
	if err := os.MkdirAll(helperDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	engine.resolveExecutablePath = func() (string, error) {
		return filepath.Join(helperDir, "push_images"), nil
	}

	writtenLayoutPath := ""
	engine.loadOCILayout = func(path string) (layout.Path, error) {
		writtenLayoutPath = path
		return layout.Path(path), nil
	}

	manifest, err := pushspec.GeneratePushManifest([]pushspec.ArchiveSpec{{
		Image:     "busybox:1.36",
		Target:    "library/busybox:1.36",
		OCIDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	}})
	if err != nil {
		t.Fatalf("pushspec.GeneratePushManifest() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(helperDir, pushspec.PushManifestFileName()), []byte(manifest), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if err := pushImagesWithEngineForTest(t, engine, Options{Registry: "registry.local:5000", InputDir: "", Concurrency: 2, All: true}); err != nil {
		t.Fatalf("PushImages() error = %v", err)
	}

	wantLayoutPath := filepath.Join(helperDir, pushspec.OCILayoutDirName())
	if writtenLayoutPath != wantLayoutPath {
		t.Fatalf("layout path = %q, want %q", writtenLayoutPath, wantLayoutPath)
	}
}

func TestPushImagesFallsBackToWorkingDirWhenExecutableDirHasNoManifest(t *testing.T) {
	probeClient := withRegistryProbeClient(t, func(*http.Request) (*http.Response, error) {
		return registryProbeResponse(http.StatusOK, "application/json", ""), nil
	})
	engine := newPushTestEngine(probeClient)
	engine.copyImageToRegistry = func(_ context.Context, _ string, _ bool, _ layout.Path, _, _, _ string) error {
		return nil
	}

	baseDir := t.TempDir()
	workingDir := filepath.Join(baseDir, "work")
	helperDir := filepath.Join(baseDir, "helper")
	if err := os.MkdirAll(workingDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() working dir error = %v", err)
	}
	if err := os.MkdirAll(helperDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() helper dir error = %v", err)
	}
	t.Chdir(workingDir)

	engine.resolveExecutablePath = func() (string, error) {
		return filepath.Join(helperDir, "push_images"), nil
	}

	writtenLayoutPath := ""
	engine.loadOCILayout = func(path string) (layout.Path, error) {
		writtenLayoutPath = path
		return layout.Path(path), nil
	}

	manifest, err := pushspec.GeneratePushManifest([]pushspec.ArchiveSpec{{
		Image:     "busybox:1.36",
		Target:    "library/busybox:1.36",
		OCIDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	}})
	if err != nil {
		t.Fatalf("pushspec.GeneratePushManifest() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(workingDir, pushspec.PushManifestFileName()), []byte(manifest), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if err := pushImagesWithEngineForTest(t, engine, Options{Registry: "registry.local:5000", InputDir: "", Concurrency: 2, All: true}); err != nil {
		t.Fatalf("PushImages() error = %v", err)
	}

	wantLayoutPath := filepath.Join(workingDir, pushspec.OCILayoutDirName())
	if writtenLayoutPath != wantLayoutPath {
		t.Fatalf("layout path = %q, want %q", writtenLayoutPath, wantLayoutPath)
	}
}

func TestCopyImageToRegistrySupportsDigestReferences(t *testing.T) {
	engine := NewEngine()
	var gotHash v1.Hash
	var gotDestRef name.Reference
	engine.loadLayoutImage = func(_ layout.Path, hash v1.Hash) (v1.Image, error) {
		gotHash = hash
		return nil, nil
	}

	engine.writeRemoteImage = func(ref name.Reference, _ v1.Image, _ ...remote.Option) error {
		gotDestRef = ref
		return nil
	}

	if err := engine.copyImageToRegistryUsingGoContainerRegistry(
		context.Background(),
		"registry.local:5000",
		false,
		layout.Path("/tmp/layout"),
		"quay.io/example/api@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"example/api@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	); err != nil {
		t.Fatalf("engine.copyImageToRegistryUsingGoContainerRegistry() error = %v", err)
	}

	if gotHash.String() != "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" {
		t.Fatalf("layout hash = %v", gotHash)
	}
	if gotDestRef == nil || gotDestRef.String() != "registry.local:5000/example/api@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" {
		t.Fatalf("destination reference = %v", gotDestRef)
	}
}

func TestCopyImageToRegistryReportsWebsiteLikeRegistryErrors(t *testing.T) {
	engine := NewEngine()
	engine.loadLayoutImage = func(_ layout.Path, _ v1.Hash) (v1.Image, error) {
		return nil, nil
	}
	engine.writeRemoteImage = func(_ name.Reference, _ v1.Image, _ ...remote.Option) error {
		return fmt.Errorf(`unexpected media type "text/html"`)
	}

	err := engine.copyImageToRegistryUsingGoContainerRegistry(
		context.Background(),
		"example.com",
		false,
		layout.Path("/tmp/layout"),
		"quay.io/example/api:v1",
		"example/api:v1",
		"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	)
	if err == nil {
		t.Fatal("engine.copyImageToRegistryUsingGoContainerRegistry() error = nil, want website error")
	}
	if !strings.Contains(err.Error(), "does not look like a container registry") {
		t.Fatalf("error = %v", err)
	}
}

func TestCopyImageToRegistryPreservesRegistry404Errors(t *testing.T) {
	engine := NewEngine()
	engine.loadLayoutImage = func(_ layout.Path, _ v1.Hash) (v1.Image, error) {
		return nil, nil
	}
	engine.writeRemoteImage = func(_ name.Reference, _ v1.Image, _ ...remote.Option) error {
		return fmt.Errorf("unexpected status code 404 Not Found")
	}

	err := engine.copyImageToRegistryUsingGoContainerRegistry(
		context.Background(),
		"registry.local:5000",
		false,
		layout.Path("/tmp/layout"),
		"quay.io/example/api:v1",
		"example/api:v1",
		"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	)
	if err == nil {
		t.Fatal("engine.copyImageToRegistryUsingGoContainerRegistry() error = nil, want registry 404 error")
	}
	if strings.Contains(err.Error(), "does not look like a container registry") {
		t.Fatalf("registry 404 was misclassified as website: %v", err)
	}
	if !strings.Contains(err.Error(), "push image") {
		t.Fatalf("expected wrapped push error, got: %v", err)
	}
}

func TestPushImagesReportsProgress(t *testing.T) {
	probeClient := withRegistryProbeClient(t, func(*http.Request) (*http.Response, error) {
		return registryProbeResponse(http.StatusOK, "application/json", ""), nil
	})
	engine := newPushTestEngine(probeClient)
	engine.copyImageToRegistry = func(_ context.Context, _ string, _ bool, _ layout.Path, _, _, _ string) error {
		return nil
	}
	engine.loadOCILayout = func(path string) (layout.Path, error) {
		return layout.Path(path), nil
	}
	engine.resolveExecutablePath = func() (string, error) {
		return "/unused/helper", nil
	}

	dir := t.TempDir()
	manifest, err := pushspec.GeneratePushManifest([]pushspec.ArchiveSpec{{
		Image:     "busybox:1.36",
		Target:    "library/busybox:1.36",
		OCIDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	}})
	if err != nil {
		t.Fatalf("pushspec.GeneratePushManifest() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, pushspec.PushManifestFileName()), []byte(manifest), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	status := new(bytes.Buffer)
	if err := pushImagesWithEngineForTest(t, engine, Options{Registry: "registry.local:5000", InputDir: dir, Concurrency: 1, All: true}, status); err != nil {
		t.Fatalf("PushImages() error = %v", err)
	}

	got := status.String()
	if strings.Contains(got, "+1 more") {
		t.Fatalf("PushImages() status unexpectedly summarized images: %q", got)
	}
	if strings.Contains(got, "\x1b[") || strings.Contains(got, "\r") {
		t.Fatalf("PushImages() status unexpectedly used terminal control codes: %q", got)
	}
	if !strings.Contains(got, "pushing 1/1: busybox:1.36") {
		t.Fatalf("PushImages() status = %q", got)
	}
}

func TestPushImagesInteractiveRequiresTerminal(t *testing.T) {
	probeClient := withRegistryProbeClient(t, func(*http.Request) (*http.Response, error) {
		return registryProbeResponse(http.StatusOK, "application/json", ""), nil
	})
	engine := newPushTestEngine(probeClient)
	// Mock network operations so they don't run
	engine.copyImageToRegistry = func(_ context.Context, _ string, _ bool, _ layout.Path, _, _, _ string) error {
		return nil
	}
	engine.loadOCILayout = func(path string) (layout.Path, error) {
		return layout.Path(path), nil
	}
	engine.resolveExecutablePath = func() (string, error) {
		return "/unused/helper", nil
	}

	dir := t.TempDir()
	manifest, err := pushspec.GeneratePushManifest([]pushspec.ArchiveSpec{
		{
			Image:     "busybox:1.36",
			Target:    "library/busybox:1.36",
			OCIDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		},
	})
	if err != nil {
		t.Fatalf("pushspec.GeneratePushManifest() error = %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, pushspec.PushManifestFileName()), []byte(manifest), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	// Use strings.NewReader which is not a terminal
	err = pushImagesWithEngineForTest(t, engine, Options{
		Registry:    "registry.local:5000",
		InputDir:    dir,
		Concurrency: 1,
		All:         false,
		In:          strings.NewReader(""),
	})

	if err == nil {
		t.Fatalf("PushImages() error = nil, want non-nil error for non-terminal input")
	}

	if !strings.Contains(err.Error(), "requires terminal input and output") {
		t.Fatalf("PushImages() error = %v, want message containing 'requires terminal input and output'", err)
	}
}

func TestEngineUsesTerminalSeamForRegistryPrompt(t *testing.T) {
	probeClient := withRegistryProbeClient(t, func(*http.Request) (*http.Response, error) {
		return registryProbeResponse(http.StatusOK, "application/json", ""), nil
	})
	engine := newPushTestEngine(probeClient)
	engine.isInteractive = func(io.Reader, io.Writer) bool { return true }

	err := pushImagesWithEngineForTest(t, engine, Options{
		InputDir:    t.TempDir(),
		Concurrency: 1,
		All:         true,
		In:          strings.NewReader("registry.local:5000\n"),
		Out:         &bytes.Buffer{},
	})
	if err == nil {
		t.Fatal("PushImages() error = nil, want missing manifest error")
	}
	if strings.Contains(err.Error(), "registry argument is required") {
		t.Fatalf("PushImages() ignored Engine.isInteractive override: %v", err)
	}
	if !strings.Contains(err.Error(), "read push manifest") {
		t.Fatalf("PushImages() error = %v, want missing manifest error after registry prompt", err)
	}
}

func TestEngineUsesTerminalSeamForImageSelection(t *testing.T) {
	probeClient := withRegistryProbeClient(t, func(*http.Request) (*http.Response, error) {
		return registryProbeResponse(http.StatusOK, "application/json", ""), nil
	})
	engine := newPushTestEngine(probeClient)
	engine.isInteractive = func(io.Reader, io.Writer) bool { return false }
	selectionCalled := false
	engine.selectImagesToPush = func(context.Context, Options, string, []pushspec.ArchiveSpec) ([]pushspec.ArchiveSpec, bool, error) {
		selectionCalled = true
		return nil, false, nil
	}
	engine.loadOCILayout = func(path string) (layout.Path, error) {
		return layout.Path(path), nil
	}

	dir := t.TempDir()
	manifest, err := pushspec.GeneratePushManifest([]pushspec.ArchiveSpec{{
		Image:     "busybox:1.36",
		Target:    "library/busybox:1.36",
		OCIDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	}})
	if err != nil {
		t.Fatalf("pushspec.GeneratePushManifest() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, pushspec.PushManifestFileName()), []byte(manifest), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	err = pushImagesWithEngineForTest(t, engine, Options{
		Registry:    "registry.local:5000",
		InputDir:    dir,
		Concurrency: 1,
		All:         false,
		In:          strings.NewReader(""),
		Out:         &bytes.Buffer{},
	})
	if err == nil || !strings.Contains(err.Error(), "requires terminal input and output") {
		t.Fatalf("PushImages() error = %v, want terminal selection error", err)
	}
	if selectionCalled {
		t.Fatal("PushImages() called image selection after Engine.isInteractive rejected the streams")
	}
}

func TestPushImagesRejectsUnreachableRegistryBeforeSelection(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}

	registry := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	err = pushImagesForTest(t, nil, Options{
		Registry:    registry,
		InputDir:    t.TempDir(),
		Concurrency: 1,
		All:         false,
		In:          strings.NewReader(""),
		Out:         &bytes.Buffer{},
	})
	if err == nil {
		t.Fatal("PushImages() error = nil, want unreachable registry error")
	}
	if !strings.Contains(err.Error(), "not reachable") {
		t.Fatalf("PushImages() error = %v, want reachability error", err)
	}
}

func TestPushImagesRejectsWebsiteLikeRegistryBeforeSelection(t *testing.T) {
	// A generic website answers /v2/ with a non-registry status and an HTML body
	// and carries no registry signal, so the preflight probe fails and the error
	// is shaped into a friendly "looks like a website" message.
	probeClient := withRegistryProbeClient(t, func(*http.Request) (*http.Response, error) {
		return websiteProbeResponse(http.StatusNotFound, "text/html; charset=utf-8", "<!doctype html><html><body>hello</body></html>"), nil
	})

	err := pushImagesForTest(t, probeClient, Options{
		Registry:    "registry.local:5000",
		InputDir:    t.TempDir(),
		Concurrency: 1,
		All:         false,
		In:          strings.NewReader(""),
		Out:         &bytes.Buffer{},
	})
	if err == nil {
		t.Fatal("PushImages() error = nil, want website-like registry error")
	}
	if !strings.Contains(err.Error(), "looks like a website") {
		t.Fatalf("PushImages() error = %v, want website-like error", err)
	}
}

func TestPushImagesAcceptsRegistryServingHTMLErrorBody(t *testing.T) {
	// Quay answers the /v2/ probe with a 401, an HTML error body, and the
	// canonical Docker-Distribution-Api-Version header. The registry signal must
	// win over the website heuristic so the preflight does not reject it. The
	// terminal-selection error here proves the preflight already passed.
	resp := registryProbeResponse(http.StatusUnauthorized, "text/html; charset=utf-8", "true")
	resp.Header.Set("Www-Authenticate", `Bearer realm="https://quay.io/v2/auth",service="quay.io"`)
	probeClient := withRegistryProbeClient(t, func(*http.Request) (*http.Response, error) {
		return resp, nil
	})
	engine := newPushTestEngine(probeClient)

	engine.loadOCILayout = func(path string) (layout.Path, error) {
		return layout.Path(path), nil
	}

	dir := t.TempDir()
	manifest, err := pushspec.GeneratePushManifest([]pushspec.ArchiveSpec{{
		Image:     "busybox:1.36",
		Target:    "library/busybox:1.36",
		OCIDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	}})
	if err != nil {
		t.Fatalf("pushspec.GeneratePushManifest() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, pushspec.PushManifestFileName()), []byte(manifest), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	err = pushImagesWithEngineForTest(t, engine, Options{
		Registry:    "quay.io",
		InputDir:    dir,
		Concurrency: 1,
		All:         false,
		In:          strings.NewReader(""),
		Out:         &bytes.Buffer{},
	})
	if err == nil {
		t.Fatal("PushImages() error = nil, want terminal selection error after successful preflight")
	}
	if strings.Contains(err.Error(), "looks like a website") {
		t.Fatalf("PushImages() rejected registry serving HTML error body as a website: %v", err)
	}
	if !strings.Contains(err.Error(), "requires terminal input and output") {
		t.Fatalf("PushImages() error = %v, want terminal selection error", err)
	}
}

func TestPushImagesAcceptsRegistryPreflightBeforeSelection(t *testing.T) {
	probeClient := withRegistryProbeClient(t, func(*http.Request) (*http.Response, error) {
		return registryProbeResponse(http.StatusUnauthorized, "application/json", ""), nil
	})
	engine := newPushTestEngine(probeClient)

	engine.loadOCILayout = func(path string) (layout.Path, error) {
		return layout.Path(path), nil
	}

	dir := t.TempDir()
	manifest, err := pushspec.GeneratePushManifest([]pushspec.ArchiveSpec{{
		Image:     "busybox:1.36",
		Target:    "library/busybox:1.36",
		OCIDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	}})
	if err != nil {
		t.Fatalf("pushspec.GeneratePushManifest() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, pushspec.PushManifestFileName()), []byte(manifest), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	err = pushImagesWithEngineForTest(t, engine, Options{
		Registry:    "registry.local:5000",
		InputDir:    dir,
		Concurrency: 1,
		All:         false,
		In:          strings.NewReader(""),
		Out:         &bytes.Buffer{},
	})
	if err == nil {
		t.Fatal("PushImages() error = nil, want terminal selection error after successful preflight")
	}
	if !strings.Contains(err.Error(), "requires terminal input and output") {
		t.Fatalf("PushImages() error = %v, want terminal selection error", err)
	}
}

func TestPushImagesPreflightUsesRegistryHostWithoutNamespacePath(t *testing.T) {
	var probeURL string
	probeClient := withRegistryProbeClient(t, func(req *http.Request) (*http.Response, error) {
		probeURL = req.URL.String()
		return registryProbeResponse(http.StatusOK, "application/json", ""), nil
	})

	err := pushImagesForTest(t, probeClient, Options{
		Registry:    "registry.local:5000/team/sub",
		InputDir:    t.TempDir(),
		Concurrency: 1,
		All:         true,
	})
	if err == nil {
		t.Fatal("PushImages() error = nil, want push manifest read error")
	}

	if probeURL != "https://registry.local:5000/v2/" {
		t.Fatalf("registry probe URL = %q, want %q", probeURL, "https://registry.local:5000/v2/")
	}
}

func TestCopyImageToRegistryAllowInsecureHTTPUsesHTTPReference(t *testing.T) {
	engine := NewEngine()
	engine.loadLayoutImage = func(_ layout.Path, _ v1.Hash) (v1.Image, error) {
		return nil, nil
	}

	var gotScheme string
	engine.writeRemoteImage = func(ref name.Reference, _ v1.Image, _ ...remote.Option) error {
		gotScheme = ref.Context().Scheme()
		return nil
	}

	if err := engine.copyImageToRegistryUsingGoContainerRegistry(
		context.Background(),
		"registry.local:5000",
		true,
		layout.Path("/tmp/layout"),
		"quay.io/example/api:v1",
		"example/api:v1",
		"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	); err != nil {
		t.Fatalf("engine.copyImageToRegistryUsingGoContainerRegistry() error = %v", err)
	}
	if gotScheme != "http" {
		t.Fatalf("destination reference scheme = %q, want %q", gotScheme, "http")
	}
}

func TestPushImagesSuggestsAllowInsecureHTTPFlagForHTTPRegistry(t *testing.T) {
	probeClient := withRegistryProbeClient(t, func(*http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("Get \"https://registry.local:5000/v2/\": http: server gave HTTP response to HTTPS client")
	})

	err := pushImagesForTest(t, probeClient, Options{
		Registry:    "registry.local:5000",
		InputDir:    t.TempDir(),
		Concurrency: 1,
		All:         false,
		In:          strings.NewReader(""),
		Out:         &bytes.Buffer{},
	})
	if err == nil {
		t.Fatal("PushImages() error = nil, want insecure-http hint error")
	}
	if !strings.Contains(err.Error(), "--allow-insecure-http") {
		t.Fatalf("PushImages() error = %v, want --allow-insecure-http hint", err)
	}
}

func TestPushImagesAllowInsecureHTTPProbesOverHTTP(t *testing.T) {
	// With --allow-insecure-http the preflight, like the actual push, still
	// attempts HTTPS first and falls back to HTTP, so an HTTP-only registry is
	// reachable without TLS.
	var schemes []string
	probeClient := withRegistryProbeClient(t, func(req *http.Request) (*http.Response, error) {
		schemes = append(schemes, req.URL.Scheme)
		if req.URL.Scheme == "https" {
			return nil, errors.New(`Get "https://registry.local:5000/v2/": dial tcp: connection refused`)
		}
		return registryProbeResponse(http.StatusOK, "application/json", ""), nil
	})

	err := pushImagesForTest(t, probeClient, Options{
		Registry:          "registry.local:5000/team/sub",
		AllowInsecureHTTP: true,
		InputDir:          t.TempDir(),
		Concurrency:       1,
		All:               true,
	})
	if err == nil {
		t.Fatal("PushImages() error = nil, want push manifest read error")
	}
	if strings.Contains(err.Error(), "preflight") {
		t.Fatalf("PushImages() error = %v, want to have passed preflight over HTTP", err)
	}

	probedHTTP := false
	for _, scheme := range schemes {
		if scheme == "http" {
			probedHTTP = true
		}
	}
	if !probedHTTP {
		t.Fatalf("registry probe schemes = %v, want an http probe when --allow-insecure-http is set", schemes)
	}
}

func TestPushImagesRejectsManifestLayoutDirTraversal(t *testing.T) {
	probeClient := withRegistryProbeClient(t, func(*http.Request) (*http.Response, error) {
		return registryProbeResponse(http.StatusOK, "application/json", ""), nil
	})
	engine := newPushTestEngine(probeClient)

	engine.resolveExecutablePath = func() (string, error) {
		return "/unused/helper", nil
	}

	dir := t.TempDir()
	manifest := pushspec.PushManifest{
		LayoutDir: "../outside-layout",
		Images: []pushspec.ArchiveSpec{
			{
				Image:     "busybox:1.36",
				Target:    "library/busybox:1.36",
				OCIDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			},
		},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, pushspec.PushManifestFileName()), append(data, '\n'), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	err = pushImagesWithEngineForTest(t, engine, Options{Registry: "registry.local:5000", InputDir: dir, Concurrency: 1, All: true})
	if err == nil {
		t.Fatal("PushImages() error = nil, want layoutDir traversal error")
	}
	if !strings.Contains(err.Error(), "layoutDir") {
		t.Fatalf("PushImages() error = %v", err)
	}
}

func TestPushImagesRejectsManifestLayoutDirAbsolutePath(t *testing.T) {
	probeClient := withRegistryProbeClient(t, func(*http.Request) (*http.Response, error) {
		return registryProbeResponse(http.StatusOK, "application/json", ""), nil
	})
	engine := newPushTestEngine(probeClient)

	engine.resolveExecutablePath = func() (string, error) {
		return "/unused/helper", nil
	}

	dir := t.TempDir()
	manifest := pushspec.PushManifest{
		LayoutDir: filepath.Join(string(filepath.Separator), "tmp", "layout"),
		Images: []pushspec.ArchiveSpec{
			{
				Image:     "busybox:1.36",
				Target:    "library/busybox:1.36",
				OCIDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			},
		},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, pushspec.PushManifestFileName()), append(data, '\n'), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	err = pushImagesWithEngineForTest(t, engine, Options{Registry: "registry.local:5000", InputDir: dir, Concurrency: 1, All: true})
	if err == nil {
		t.Fatal("PushImages() error = nil, want absolute layoutDir error")
	}
	if !strings.Contains(err.Error(), "layoutDir") {
		t.Fatalf("PushImages() error = %v", err)
	}
}

func TestSelectedConflicts(t *testing.T) {
	selected := []pushspec.ArchiveSpec{
		{Image: "a:v1", Target: "a:v1", OCIDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{Image: "b:v1", Target: "b:v1", OCIDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
	}
	classified := []classifiedImage{
		{Spec: selected[0], Status: statusConflict, RemoteDigest: "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"},
		{Spec: selected[1], Status: statusMirrored},
		{Spec: pushspec.ArchiveSpec{Image: "c:v1", Target: "c:v1", OCIDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}, Status: statusConflict},
	}

	got := selectedConflicts(selected, classified)
	if len(got) != 1 {
		t.Fatalf("selectedConflicts() len = %d, want 1", len(got))
	}
	if got[0].Spec.Target != "a:v1" {
		t.Fatalf("selectedConflicts()[0].Spec.Target = %q, want %q", got[0].Spec.Target, "a:v1")
	}
}

func TestConfirmConflictSelection(t *testing.T) {
	t.Run("yes", func(t *testing.T) {
		out := new(bytes.Buffer)
		confirmed, err := confirmConflictSelection(strings.NewReader("yes\n"), out, []classifiedImage{
			{
				Spec:         pushspec.ArchiveSpec{Target: "a:v1", OCIDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
				Status:       statusConflict,
				RemoteDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			},
		})
		if err != nil {
			t.Fatalf("confirmConflictSelection() error = %v", err)
		}
		if !confirmed {
			t.Fatal("confirmConflictSelection() confirmed = false, want true")
		}
		if !strings.Contains(out.String(), "WARNING:") {
			t.Fatalf("warning output missing, got: %q", out.String())
		}
		if !strings.Contains(out.String(), "current=sha256:bbbb") || !strings.Contains(out.String(), "staged=sha256:aaaa") {
			t.Fatalf("digest details missing in warning output: %q", out.String())
		}
	})

	t.Run("no", func(t *testing.T) {
		out := new(bytes.Buffer)
		confirmed, err := confirmConflictSelection(strings.NewReader("no\n"), out, []classifiedImage{
			{Spec: pushspec.ArchiveSpec{Target: "a:v1", OCIDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, Status: statusConflict, RemoteDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
		})
		if err != nil {
			t.Fatalf("confirmConflictSelection() error = %v", err)
		}
		if confirmed {
			t.Fatal("confirmConflictSelection() confirmed = true, want false")
		}
	})

	t.Run("invalid then yes", func(t *testing.T) {
		out := new(bytes.Buffer)
		confirmed, err := confirmConflictSelection(strings.NewReader("maybe\ny\n"), out, []classifiedImage{
			{Spec: pushspec.ArchiveSpec{Target: "a:v1", OCIDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, Status: statusConflict, RemoteDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
		})
		if err != nil {
			t.Fatalf("confirmConflictSelection() error = %v", err)
		}
		if !confirmed {
			t.Fatal("confirmConflictSelection() confirmed = false, want true")
		}
		if !strings.Contains(out.String(), "Please type yes or no.") {
			t.Fatalf("expected retry hint, got: %q", out.String())
		}
	})
}
