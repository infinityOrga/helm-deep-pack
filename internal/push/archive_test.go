package push

import (
	"bytes"
	"context"
	"errors"
	"helm-deep-pack/internal/pushspec"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

func TestArchiveImagesCreatesDigestSpecs(t *testing.T) {
	originalFetch := fetchRemoteImage
	originalWriteLayout := writeLayout
	originalFromLayout := fromLayoutPath
	originalAppend := appendLayoutImage
	originalWrite := writeLayoutImage
	defer func() {
		fetchRemoteImage = originalFetch
		writeLayout = originalWriteLayout
		fromLayoutPath = originalFromLayout
		appendLayoutImage = originalAppend
		writeLayoutImage = originalWrite
	}()

	fetchRemoteImage = func(ref name.Reference, _ ...remote.Option) (v1.Image, error) {
		return fakeImageWithDigest(t, map[string]string{
			"quay.io/example/api:v1": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"busybox:1.36":           "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		}[ref.String()]), nil
	}

	var layoutPath string
	writeLayout = func(path string, _ v1.ImageIndex) (layout.Path, error) {
		layoutPath = path
		return layout.Path(path), nil
	}
	fromLayoutPath = func(path string) (layout.Path, error) {
		return layout.Path(path), nil
	}

	var appended int
	writeLayoutImage = func(_ layout.Path, _ v1.Image) error { return nil }
	appendLayoutImage = func(_ layout.Path, _ v1.Image) error {
		appended++
		return nil
	}

	specs, err := ArchiveImages(context.Background(), []string{"quay.io/example/api:v1", "busybox:1.36"}, t.TempDir(), 4)
	if err != nil {
		t.Fatalf("ArchiveImages() error = %v", err)
	}

	if filepath.Base(layoutPath) != pushspec.OCILayoutDirName() {
		t.Fatalf("ArchiveImages() layout path = %q, want suffix %q", layoutPath, pushspec.OCILayoutDirName())
	}
	if appended != 2 {
		t.Fatalf("ArchiveImages() appended = %d, want 2", appended)
	}
	if len(specs) != 2 {
		t.Fatalf("ArchiveImages() specs len = %d, want 2", len(specs))
	}
	if specs[0].OCIDigest == "" || specs[1].OCIDigest == "" {
		t.Fatalf("ArchiveImages() missing digests: %#v", specs)
	}
}

func TestArchiveImagesFailsOnCopyError(t *testing.T) {
	originalFetch := fetchRemoteImage
	originalWriteLayout := writeLayout
	originalFromLayout := fromLayoutPath
	defer func() {
		fetchRemoteImage = originalFetch
		writeLayout = originalWriteLayout
		fromLayoutPath = originalFromLayout
	}()

	writeLayout = func(path string, _ v1.ImageIndex) (layout.Path, error) {
		return layout.Path(path), nil
	}
	fromLayoutPath = func(path string) (layout.Path, error) {
		return layout.Path(path), nil
	}

	fetchRemoteImage = func(_ name.Reference, _ ...remote.Option) (v1.Image, error) {
		return nil, errors.New("boom")
	}

	_, err := ArchiveImages(context.Background(), []string{"busybox:1.36"}, t.TempDir(), 1)
	if err == nil {
		t.Fatal("ArchiveImages() error = nil, want error")
	}
}

func TestArchiveImagesBestEffortReturnsSuccessfulSpecsAndFailures(t *testing.T) {
	originalFetch := fetchRemoteImage
	originalWriteLayout := writeLayout
	originalFromLayout := fromLayoutPath
	originalAppend := appendLayoutImage
	originalWrite := writeLayoutImage
	defer func() {
		fetchRemoteImage = originalFetch
		writeLayout = originalWriteLayout
		fromLayoutPath = originalFromLayout
		appendLayoutImage = originalAppend
		writeLayoutImage = originalWrite
	}()

	fetchRemoteImage = func(ref name.Reference, _ ...remote.Option) (v1.Image, error) {
		if ref.String() == "quay.io/example/missing:v1" {
			return nil, errors.New("image unavailable")
		}
		return fakeImageWithDigest(t, "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), nil
	}
	writeLayout = func(path string, _ v1.ImageIndex) (layout.Path, error) {
		return layout.Path(path), nil
	}
	fromLayoutPath = func(path string) (layout.Path, error) {
		return layout.Path(path), nil
	}
	writeLayoutImage = func(_ layout.Path, _ v1.Image) error { return nil }
	appendLayoutImage = func(_ layout.Path, _ v1.Image) error { return nil }

	specs, failures, err := ArchiveImagesBestEffort(context.Background(), []string{
		"quay.io/example/missing:v1",
		"quay.io/example/available:v1",
	}, t.TempDir(), 2)
	if err != nil {
		t.Fatalf("ArchiveImagesBestEffort() error = %v", err)
	}
	if len(specs) != 1 || specs[0].Image != "quay.io/example/available:v1" {
		t.Fatalf("ArchiveImagesBestEffort() specs = %#v, want the available image only", specs)
	}
	if len(failures) != 1 || failures[0].Image != "quay.io/example/missing:v1" {
		t.Fatalf("ArchiveImagesBestEffort() failures = %#v, want the missing image only", failures)
	}
	if !strings.Contains(failures[0].Err.Error(), "image unavailable") {
		t.Fatalf("ArchiveImagesBestEffort() failure = %v, want source error", failures[0].Err)
	}
}

func TestArchiveImagesSupportsDigestReferences(t *testing.T) {
	originalFetch := fetchRemoteImage
	originalWriteLayout := writeLayout
	originalFromLayout := fromLayoutPath
	originalAppend := appendLayoutImage
	originalWrite := writeLayoutImage
	defer func() {
		fetchRemoteImage = originalFetch
		writeLayout = originalWriteLayout
		fromLayoutPath = originalFromLayout
		appendLayoutImage = originalAppend
		writeLayoutImage = originalWrite
	}()

	var gotRef name.Reference
	fetchRemoteImage = func(ref name.Reference, _ ...remote.Option) (v1.Image, error) {
		gotRef = ref
		return fakeImageWithDigest(t, "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"), nil
	}
	writeLayout = func(path string, _ v1.ImageIndex) (layout.Path, error) {
		return layout.Path(path), nil
	}
	fromLayoutPath = func(path string) (layout.Path, error) {
		return layout.Path(path), nil
	}
	writeLayoutImage = func(_ layout.Path, _ v1.Image) error { return nil }
	appendLayoutImage = func(_ layout.Path, _ v1.Image) error {
		return nil
	}

	specs, err := ArchiveImages(context.Background(), []string{"quay.io/example/api@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}, t.TempDir(), 2)
	if err != nil {
		t.Fatalf("ArchiveImages() error = %v", err)
	}

	if gotRef == nil || gotRef.String() != "quay.io/example/api@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" {
		t.Fatalf("fetchRemoteImage() ref = %v", gotRef)
	}
	if specs[0].OCIDigest == "" {
		t.Fatal("ArchiveImages() digest is empty")
	}
}

func TestArchiveImagesAppendsToExistingLayout(t *testing.T) {
	originalFetch := fetchRemoteImage
	originalWriteLayout := writeLayout
	originalFromLayout := fromLayoutPath
	originalAppend := appendLayoutImage
	originalWrite := writeLayoutImage
	defer func() {
		fetchRemoteImage = originalFetch
		writeLayout = originalWriteLayout
		fromLayoutPath = originalFromLayout
		appendLayoutImage = originalAppend
		writeLayoutImage = originalWrite
	}()

	fetchRemoteImage = func(ref name.Reference, _ ...remote.Option) (v1.Image, error) {
		return fakeImageWithDigest(t, map[string]string{
			"busybox:1.36": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		}[ref.String()]), nil
	}

	writeCalled := false
	writeLayout = func(path string, _ v1.ImageIndex) (layout.Path, error) {
		writeCalled = true
		return layout.Path(path), nil
	}
	fromLayoutPath = func(path string) (layout.Path, error) {
		return layout.Path(path), nil
	}
	writeLayoutImage = func(_ layout.Path, _ v1.Image) error { return nil }
	writeLayoutImage = func(_ layout.Path, _ v1.Image) error { return nil }
	appendLayoutImage = func(_ layout.Path, _ v1.Image) error { return nil }

	out := t.TempDir()
	if err := os.MkdirAll(filepath.Join(out, pushspec.OCILayoutDirName()), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	specs, err := ArchiveImages(context.Background(), []string{"busybox:1.36"}, out, 1)
	if err != nil {
		t.Fatalf("ArchiveImages() error = %v", err)
	}
	if writeCalled {
		t.Fatal("ArchiveImages() unexpectedly created new layout for existing layout dir")
	}
	if len(specs) != 1 || specs[0].OCIDigest == "" {
		t.Fatalf("ArchiveImages() specs = %#v", specs)
	}
}

func TestArchiveImagesReportsProgress(t *testing.T) {
	originalFetch := fetchRemoteImage
	originalWriteLayout := writeLayout
	originalFromLayout := fromLayoutPath
	originalAppend := appendLayoutImage
	originalWrite := writeLayoutImage
	defer func() {
		fetchRemoteImage = originalFetch
		writeLayout = originalWriteLayout
		fromLayoutPath = originalFromLayout
		appendLayoutImage = originalAppend
		writeLayoutImage = originalWrite
	}()

	fetchRemoteImage = func(ref name.Reference, _ ...remote.Option) (v1.Image, error) {
		return fakeImageWithDigest(t, map[string]string{
			"quay.io/example/api:v1": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"busybox:1.36":           "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		}[ref.String()]), nil
	}
	writeLayout = func(path string, _ v1.ImageIndex) (layout.Path, error) {
		return layout.Path(path), nil
	}
	fromLayoutPath = func(path string) (layout.Path, error) {
		return layout.Path(path), nil
	}
	writeLayoutImage = func(_ layout.Path, _ v1.Image) error { return nil }
	appendLayoutImage = func(_ layout.Path, _ v1.Image) error {
		return nil
	}

	status := new(bytes.Buffer)
	if _, err := ArchiveImages(context.Background(), []string{"quay.io/example/api:v1", "busybox:1.36"}, t.TempDir(), 1, status); err != nil {
		t.Fatalf("ArchiveImages() error = %v", err)
	}

	got := status.String()
	if strings.Contains(got, "+1 more") {
		t.Fatalf("ArchiveImages() status unexpectedly summarized images: %q", got)
	}
	if strings.Contains(got, "\x1b[") || strings.Contains(got, "\r") {
		t.Fatalf("ArchiveImages() status unexpectedly used terminal control codes: %q", got)
	}
	if !strings.Contains(got, "pulling 1/2:") || !strings.Contains(got, "pulling 2/2:") ||
		!strings.Contains(got, "quay.io/example/api:v1") || !strings.Contains(got, "busybox:1.36") {
		t.Fatalf("ArchiveImages() status = %q", got)
	}
}

type fakeImage struct {
	digest   v1.Hash
	manifest *v1.Manifest
}

func (f fakeImage) Digest() (v1.Hash, error) {
	return f.digest, nil
}

func (f fakeImage) Manifest() (*v1.Manifest, error) {
	return f.manifest, nil
}

func (f fakeImage) Layers() ([]v1.Layer, error) {
	return nil, nil
}

func (f fakeImage) MediaType() (types.MediaType, error) {
	return "", nil
}

func (f fakeImage) Size() (int64, error) {
	if f.manifest == nil {
		return 0, nil
	}
	total := f.manifest.Config.Size
	for _, layer := range f.manifest.Layers {
		total += layer.Size
	}
	return total, nil
}

func (f fakeImage) ConfigName() (v1.Hash, error) {
	return v1.Hash{}, nil
}

func (f fakeImage) ConfigFile() (*v1.ConfigFile, error) {
	return &v1.ConfigFile{}, nil
}

func (f fakeImage) RawConfigFile() ([]byte, error) {
	return []byte("{}"), nil
}

func (f fakeImage) RawManifest() ([]byte, error) {
	return []byte("{}"), nil
}

func (f fakeImage) LayerByDigest(v1.Hash) (v1.Layer, error) {
	return nil, nil
}

func (f fakeImage) LayerByDiffID(v1.Hash) (v1.Layer, error) {
	return nil, nil
}

func fakeImageWithDigest(t *testing.T, digest string) v1.Image {
	t.Helper()
	hash, err := v1.NewHash(digest)
	if err != nil {
		t.Fatalf("NewHash(%q): %v", digest, err)
	}
	return fakeImage{
		digest: hash,
		manifest: &v1.Manifest{
			Config: v1.Descriptor{Size: 1024 * 1024},
			Layers: []v1.Descriptor{{Size: 2 * 1024 * 1024}},
		},
	}
}
