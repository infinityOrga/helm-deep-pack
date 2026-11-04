package push

import (
	"context"
	"fmt"
	"helm-deep-pack/internal/pushspec"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
)

func mustHash(t *testing.T, s string) v1.Hash {
	hash, err := v1.NewHash(s)
	if err != nil {
		t.Fatalf("v1.NewHash(%q) error = %v", s, err)
	}
	return hash
}

func TestMissingTargetStatus(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		want      targetStatus
		wantKnown bool
	}{
		{
			name: "new repository",
			err: &transport.Error{StatusCode: 404, Errors: []transport.Diagnostic{{
				Code: transport.NameUnknownErrorCode,
			}}},
			want:      statusNewRepository,
			wantKnown: true,
		},
		{
			name: "new tag",
			err: &transport.Error{StatusCode: 404, Errors: []transport.Diagnostic{{
				Code: transport.ManifestUnknownErrorCode,
			}}},
			want:      statusNewTag,
			wantKnown: true,
		},
		{
			name:      "ambiguous 404",
			err:       &transport.Error{StatusCode: 404},
			want:      statusUnknown,
			wantKnown: false,
		},
		{
			name:      "connection refused",
			err:       fmt.Errorf("dial tcp: connection refused"),
			want:      statusUnknown,
			wantKnown: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, known := missingTargetStatus(tt.err)
			if got != tt.want || known != tt.wantKnown {
				t.Fatalf("missingTargetStatus(%v) = (%v, %v), want (%v, %v)", tt.err, got, known, tt.want, tt.wantKnown)
			}
		})
	}
}

func TestClassifyImagesMirrored(t *testing.T) {
	specDigest := "sha256:" + strings.Repeat("a", 64)
	headFn := func(ref name.Reference, opts ...remote.Option) (*v1.Descriptor, error) {
		// Return descriptor with matching digest
		return &v1.Descriptor{
			Digest: mustHash(t, specDigest),
		}, nil
	}

	specs := []pushspec.ArchiveSpec{
		{
			Image:     "quay.io/example/api:v1",
			Target:    "example/api:v1",
			OCIDigest: specDigest,
		},
	}

	result := classifyImagesWithHead(context.Background(), "registry.local:5000", false, specs, headFn)

	if len(result) != 1 {
		t.Fatalf("classifyImages() returned %d images, want 1", len(result))
	}

	if result[0].Status != statusMirrored {
		t.Fatalf("Status = %v, want %v", result[0].Status, statusMirrored)
	}

	if result[0].RemoteDigest != specDigest {
		t.Fatalf("RemoteDigest = %q, want %q", result[0].RemoteDigest, specDigest)
	}

	if result[0].ProbeErr != nil {
		t.Fatalf("ProbeErr = %v, want nil", result[0].ProbeErr)
	}
}

func TestClassifyImagesConflict(t *testing.T) {
	specDigest := "sha256:" + strings.Repeat("a", 64)
	remoteDigest := "sha256:" + strings.Repeat("b", 64)
	headFn := func(ref name.Reference, opts ...remote.Option) (*v1.Descriptor, error) {
		// Return descriptor with different digest
		return &v1.Descriptor{
			Digest: mustHash(t, remoteDigest),
		}, nil
	}

	specs := []pushspec.ArchiveSpec{
		{
			Image:     "quay.io/example/api:v1",
			Target:    "example/api:v1",
			OCIDigest: specDigest,
		},
	}

	result := classifyImagesWithHead(context.Background(), "registry.local:5000", false, specs, headFn)

	if len(result) != 1 {
		t.Fatalf("classifyImages() returned %d images, want 1", len(result))
	}

	if result[0].Status != statusConflict {
		t.Fatalf("Status = %v, want %v", result[0].Status, statusConflict)
	}

	if result[0].RemoteDigest != remoteDigest {
		t.Fatalf("RemoteDigest = %q, want %q", result[0].RemoteDigest, remoteDigest)
	}
}

func TestClassifyImagesNewTag(t *testing.T) {
	specDigest := "sha256:" + strings.Repeat("a", 64)
	headFn := func(ref name.Reference, opts ...remote.Option) (*v1.Descriptor, error) {
		return nil, &transport.Error{StatusCode: 404, Errors: []transport.Diagnostic{{
			Code: transport.ManifestUnknownErrorCode,
		}}}
	}

	specs := []pushspec.ArchiveSpec{
		{
			Image:     "quay.io/example/api:v1",
			Target:    "example/api:v1",
			OCIDigest: specDigest,
		},
	}

	result := classifyImagesWithHead(context.Background(), "registry.local:5000", false, specs, headFn)

	if len(result) != 1 {
		t.Fatalf("classifyImages() returned %d images, want 1", len(result))
	}

	if result[0].Status != statusNewTag {
		t.Fatalf("Status = %v, want %v", result[0].Status, statusNewTag)
	}

	if result[0].RemoteDigest != "" {
		t.Fatalf("RemoteDigest = %q, want empty", result[0].RemoteDigest)
	}

	if result[0].ProbeErr != nil {
		t.Fatalf("ProbeErr = %v, want nil", result[0].ProbeErr)
	}
}

func TestClassifyChartUsesHelmOCINameAndVersionTag(t *testing.T) {
	bundle := &chartBundle{name: "example", version: "1.2.3+build", tag: "1.2.3_build"}
	var gotReference string
	classified := classifyChartWithHead(context.Background(), "registry.example.com/team", false, bundle, func(ref name.Reference, _ ...remote.Option) (*v1.Descriptor, error) {
		gotReference = ref.Name()
		return nil, &transport.Error{StatusCode: 404, Errors: []transport.Diagnostic{{
			Code: transport.ManifestUnknownErrorCode,
		}}}
	})
	if gotReference != "registry.example.com/team/example:1.2.3_build" {
		t.Fatalf("chart probe reference = %q", gotReference)
	}
	if classified.Status != statusNewTag {
		t.Fatalf("chart status = %v, want %v", classified.Status, statusNewTag)
	}
}

func TestClassifyImagesUnknown(t *testing.T) {
	specDigest := "sha256:" + strings.Repeat("a", 64)
	headFn := func(ref name.Reference, opts ...remote.Option) (*v1.Descriptor, error) {
		// Return a generic error (not a not-found error)
		return nil, fmt.Errorf("dial tcp: connection refused")
	}

	specs := []pushspec.ArchiveSpec{
		{
			Image:     "quay.io/example/api:v1",
			Target:    "example/api:v1",
			OCIDigest: specDigest,
		},
	}

	result := classifyImagesWithHead(context.Background(), "registry.local:5000", false, specs, headFn)

	if len(result) != 1 {
		t.Fatalf("classifyImages() returned %d images, want 1", len(result))
	}

	if result[0].Status != statusUnknown {
		t.Fatalf("Status = %v, want %v", result[0].Status, statusUnknown)
	}

	if result[0].RemoteDigest != "" {
		t.Fatalf("RemoteDigest = %q, want empty", result[0].RemoteDigest)
	}

	if result[0].ProbeErr == nil {
		t.Fatalf("ProbeErr = nil, want non-nil error")
	}
}

func TestClassifyImagesAllowInsecureHTTPUsesHTTPReference(t *testing.T) {
	specDigest := "sha256:" + strings.Repeat("a", 64)
	var gotScheme string
	headFn := func(ref name.Reference, opts ...remote.Option) (*v1.Descriptor, error) {
		gotScheme = ref.Context().Scheme()
		return nil, fmt.Errorf("manifest unknown: manifest_unknown")
	}

	specs := []pushspec.ArchiveSpec{
		{
			Image:     "quay.io/example/api:v1",
			Target:    "example/api:v1",
			OCIDigest: specDigest,
		},
	}

	result := classifyImagesWithHead(context.Background(), "registry.local:5000", true, specs, headFn)
	if len(result) != 1 {
		t.Fatalf("classifyImages() returned %d images, want 1", len(result))
	}
	if gotScheme != "http" {
		t.Fatalf("registry scheme = %q, want %q", gotScheme, "http")
	}
}
