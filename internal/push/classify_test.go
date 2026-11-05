package push

import (
	"context"
	"fmt"
	"helm-deep-pack/internal/pushspec"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/random"
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

func TestClassifyTargetAgainstRealRegistry(t *testing.T) {
	srv := httptest.NewServer(registry.New(registry.Logger(log.New(io.Discard, "", 0))))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	img, err := random.Image(16, 1)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := name.ParseReference(host+"/team/app:1", name.Insecure)
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Write(ref, img); err != nil {
		t.Fatal(err)
	}

	for target, want := range map[string]targetStatus{
		"nope/none:1": statusNewRepository,
		"team/app:2":  statusNewTag,
		"team/app:1":  statusTagExists,
	} {
		got := classifyTarget(context.Background(), host, target, true, "", probeTarget)
		if got.Status != want {
			t.Errorf("%s: status = %q (err %v), want %q", target, got.Status, got.ProbeErr, want)
		}
	}
}

func TestClassifyTargetBareNotFoundIsMissing(t *testing.T) {
	notFound := func(name.Reference, ...remote.Option) (*v1.Descriptor, error) {
		return nil, fmt.Errorf("probe: %w", errMissing)
	}
	got := classifyTarget(context.Background(), "r.local:5000", "team/a:1", false, "", notFound)
	if got.Status != statusMissing || got.ProbeErr != nil {
		t.Fatalf("got %+v", got)
	}
	if label, _ := statusDisplay(statusMissing); label != "[missing]" {
		t.Fatalf("label = %q", label)
	}
}

// distributionLikeRegistry mimics distribution/registry: a missing tag is a
// body-less 404 (HEAD) or MANIFEST_UNKNOWN even when the repository does not
// exist; only the tags list distinguishes an unknown repository.
func distributionLikeRegistry(t *testing.T, digest string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/":
			return
		case "/v2/team/app/manifests/1":
			w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
			w.Header().Set("Docker-Content-Digest", digest)
			w.Header().Set("Content-Length", "2")
			return
		case "/v2/team/app/tags/list":
			_, _ = io.WriteString(w, `{"name":"team/app","tags":["1"]}`)
			return
		case "/v2/locked/app/tags/list":
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"errors":[{"code":"DENIED"}]}`)
			return
		case "/v2/robot/app/manifests/1":
			// Quay: a pull token cannot cover a repository that does not exist yet.
			w.WriteHeader(http.StatusUnauthorized)
			return
		case "/v2/broken/app/manifests/1":
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/tags/list") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"errors":[{"code":"NAME_UNKNOWN"}]}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, `{"errors":[{"code":"MANIFEST_UNKNOWN"}]}`)
		}
	}))
	t.Cleanup(server.Close)
	return strings.TrimPrefix(server.URL, "http://")
}

func TestProbeTargetAgainstDistributionLikeRegistry(t *testing.T) {
	digest := "sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a"
	host := distributionLikeRegistry(t, digest)
	probe := func(ref name.Reference, opts ...remote.Option) (*v1.Descriptor, error) {
		return probeTarget(ref, append(opts, remote.WithRetryBackoff(remote.Backoff{Steps: 1}))...)
	}
	for target, want := range map[string]targetStatus{
		"never/created:1": statusNewRepository,
		"team/app:2":      statusNewTag,
		"team/app:1":      statusTagExists,
		"locked/app:1":    statusMissing,
		"robot/app:1":     statusNewRepository,
		"broken/app:1":    statusUnknown,
	} {
		got := classifyTarget(context.Background(), host, target, true, "", probe)
		if got.Status != want {
			t.Errorf("%s: status = %q (err %v), want %q", target, got.Status, got.ProbeErr, want)
		}
	}
}
