package pushspec

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestGeneratePushManifest(t *testing.T) {
	got, err := GeneratePushManifest([]ArchiveSpec{
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
		t.Fatalf("GeneratePushManifest() error = %v", err)
	}

	var manifest PushManifest
	if err := json.Unmarshal([]byte(got), &manifest); err != nil {
		t.Fatalf("GeneratePushManifest() returned invalid JSON: %v", err)
	}

	if manifest.LayoutDir != OCILayoutDirName() {
		t.Fatalf("GeneratePushManifest() layoutDir = %q, want %q", manifest.LayoutDir, OCILayoutDirName())
	}

	if len(manifest.Images) != 2 {
		t.Fatalf("GeneratePushManifest() images len = %d, want 2", len(manifest.Images))
	}
	if manifest.Images[0].Target != "example/api:v1" {
		t.Fatalf("GeneratePushManifest() target = %q, want %q", manifest.Images[0].Target, "example/api:v1")
	}
	if manifest.Images[1].OCIDigest == "" {
		t.Fatal("GeneratePushManifest() missing oci digest")
	}
}

func TestBuildSpecsSupportsDigestReferences(t *testing.T) {
	specs, err := BuildSpecs([]string{
		"quay.io/example/api@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	})
	if err != nil {
		t.Fatalf("BuildSpecs() error = %v", err)
	}

	if len(specs) != 1 {
		t.Fatalf("BuildSpecs() len = %d, want 1", len(specs))
	}
	if specs[0].Target != "example/api@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" {
		t.Fatalf("BuildSpecs() target = %q", specs[0].Target)
	}
}

func TestGeneratePushManifestRejectsMissingDigest(t *testing.T) {
	_, err := GeneratePushManifest([]ArchiveSpec{{Image: "busybox:1.36", Target: "library/busybox:1.36"}})
	if err == nil {
		t.Fatal("GeneratePushManifest() error = nil, want error")
	}
}

func TestGeneratePushManifestPersistsOptionalMetadata(t *testing.T) {
	data, err := GeneratePushManifest([]ArchiveSpec{{
		Image:         "quay.io/example/metrics:v1",
		Target:        "example/metrics:v1",
		OCIDigest:     "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Optional:      true,
		OptionalFlags: []string{".Values.metrics.enabled"},
	}})
	if err != nil {
		t.Fatalf("GeneratePushManifest() error = %v", err)
	}

	var manifest PushManifest
	if err := json.Unmarshal([]byte(data), &manifest); err != nil {
		t.Fatalf("GeneratePushManifest() returned invalid JSON: %v", err)
	}
	if !manifest.Images[0].Optional {
		t.Fatal("optional metadata was not persisted")
	}
	if len(manifest.Images[0].OptionalFlags) != 1 || manifest.Images[0].OptionalFlags[0] != ".Values.metrics.enabled" {
		t.Fatalf("optional flags = %v, want Helm values path", manifest.Images[0].OptionalFlags)
	}
}

func TestWritePushManifestWritesManifestFile(t *testing.T) {
	dir := t.TempDir()
	specs := []ArchiveSpec{{
		Image:     "busybox:1.36",
		Target:    "library/busybox:1.36",
		OCIDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}}

	if err := WritePushManifest(dir, specs); err != nil {
		t.Fatalf("WritePushManifest() error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, PushManifestFileName()))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	var manifest PushManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("WritePushManifest() wrote invalid JSON: %v", err)
	}
	if len(manifest.Images) != 1 || manifest.Images[0].Image != "busybox:1.36" {
		t.Fatalf("WritePushManifest() images = %#v", manifest.Images)
	}
}
