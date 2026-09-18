package add

import (
	"bytes"
	"context"
	"fmt"
	"helm-deep-pack/internal/pushspec"
	"io"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name               string
		opts               Options
		existingManifest   *pushspec.PushManifest
		readManifestErr    error
		archivedSpecs      []pushspec.ArchiveSpec
		archiveImagesErr   error
		writeManifestErr   error
		wantErr            bool
		wantErrContains    string
		wantArchivedImages []string
		wantWrittenSpecs   []pushspec.ArchiveSpec
		wantStatusContains string
	}{
		{
			name: "dedupe skips existing images",
			opts: Options{
				OutputDir:   "/test/out",
				Images:      []string{"nginx:1.27", "redis:7", "postgres:14"},
				Concurrency: 2,
			},
			existingManifest: &pushspec.PushManifest{
				LayoutDir: "oci-layout",
				Images: []pushspec.ArchiveSpec{
					{Image: "nginx:1.27", Target: "library/nginx:1.27", OCIDigest: "sha256:abc"},
					{Image: "redis:7", Target: "library/redis:7", OCIDigest: "sha256:def"},
				},
			},
			archivedSpecs: []pushspec.ArchiveSpec{
				{Image: "postgres:14", Target: "library/postgres:14", OCIDigest: "sha256:ghi", Optional: true, OptionalFlags: []string{".Values.postgres.enabled"}},
			},
			wantArchivedImages: []string{"postgres:14"},
			wantWrittenSpecs: []pushspec.ArchiveSpec{
				{Image: "nginx:1.27", Target: "library/nginx:1.27", OCIDigest: "sha256:abc"},
				{Image: "redis:7", Target: "library/redis:7", OCIDigest: "sha256:def"},
				{Image: "postgres:14", Target: "library/postgres:14", OCIDigest: "sha256:ghi"},
			},
			wantStatusContains: "added 1 image(s)",
		},
		{
			name: "missing manifest errors",
			opts: Options{
				OutputDir:   "/test/out",
				Images:      []string{"nginx:1.27"},
				Concurrency: 2,
			},
			readManifestErr: fmt.Errorf("no such file"),
			wantErr:         true,
			wantErrContains: "read push manifest",
		},
		{
			name: "only new images archived",
			opts: Options{
				OutputDir:   "/test/out",
				Images:      []string{"nginx:1.27", "redis:7"},
				Concurrency: 2,
			},
			existingManifest: &pushspec.PushManifest{
				LayoutDir: "oci-layout",
				Images:    []pushspec.ArchiveSpec{},
			},
			archivedSpecs: []pushspec.ArchiveSpec{
				{Image: "nginx:1.27", Target: "library/nginx:1.27", OCIDigest: "sha256:abc"},
				{Image: "redis:7", Target: "library/redis:7", OCIDigest: "sha256:def"},
			},
			wantArchivedImages: []string{"nginx:1.27", "redis:7"},
			wantWrittenSpecs: []pushspec.ArchiveSpec{
				{Image: "nginx:1.27", Target: "library/nginx:1.27", OCIDigest: "sha256:abc"},
				{Image: "redis:7", Target: "library/redis:7", OCIDigest: "sha256:def"},
			},
			wantStatusContains: "added 2 image(s)",
		},
		{
			name: "merged manifest written",
			opts: Options{
				OutputDir:   "/test/out",
				Images:      []string{"postgres:14"},
				Concurrency: 2,
			},
			existingManifest: &pushspec.PushManifest{
				LayoutDir: "oci-layout",
				Images: []pushspec.ArchiveSpec{
					{Image: "nginx:1.27", Target: "library/nginx:1.27", OCIDigest: "sha256:abc"},
				},
			},
			archivedSpecs: []pushspec.ArchiveSpec{
				{Image: "postgres:14", Target: "library/postgres:14", OCIDigest: "sha256:ghi"},
			},
			wantArchivedImages: []string{"postgres:14"},
			wantWrittenSpecs: []pushspec.ArchiveSpec{
				{Image: "nginx:1.27", Target: "library/nginx:1.27", OCIDigest: "sha256:abc"},
				{Image: "postgres:14", Target: "library/postgres:14", OCIDigest: "sha256:ghi"},
			},
			wantStatusContains: "added 1 image(s)",
		},
		{
			name: "all-dup no-op",
			opts: Options{
				OutputDir:   "/test/out",
				Images:      []string{"nginx:1.27", "redis:7"},
				Concurrency: 2,
			},
			existingManifest: &pushspec.PushManifest{
				LayoutDir: "oci-layout",
				Images: []pushspec.ArchiveSpec{
					{Image: "nginx:1.27", Target: "library/nginx:1.27", OCIDigest: "sha256:abc"},
					{Image: "redis:7", Target: "library/redis:7", OCIDigest: "sha256:def"},
				},
			},
			wantStatusContains: "all images already present",
		},
		{
			name: "dedupe within input list",
			opts: Options{
				OutputDir:   "/test/out",
				Images:      []string{"nginx:1.27", "nginx:1.27", "redis:7"},
				Concurrency: 2,
			},
			existingManifest: &pushspec.PushManifest{
				LayoutDir: "oci-layout",
				Images:    []pushspec.ArchiveSpec{},
			},
			archivedSpecs: []pushspec.ArchiveSpec{
				{Image: "nginx:1.27", Target: "library/nginx:1.27", OCIDigest: "sha256:abc"},
				{Image: "redis:7", Target: "library/redis:7", OCIDigest: "sha256:def"},
			},
			wantArchivedImages: []string{"nginx:1.27", "redis:7"},
			wantWrittenSpecs: []pushspec.ArchiveSpec{
				{Image: "nginx:1.27", Target: "library/nginx:1.27", OCIDigest: "sha256:abc"},
				{Image: "redis:7", Target: "library/redis:7", OCIDigest: "sha256:def"},
			},
			wantStatusContains: "added 2 image(s)",
		},
		{
			name: "archive error propagates",
			opts: Options{
				OutputDir:   "/test/out",
				Images:      []string{"bad:image"},
				Concurrency: 2,
			},
			existingManifest: &pushspec.PushManifest{
				LayoutDir: "oci-layout",
				Images:    []pushspec.ArchiveSpec{},
			},
			archiveImagesErr:   fmt.Errorf("network timeout"),
			wantErr:            true,
			wantErrContains:    "archive new images",
			wantArchivedImages: []string{"bad:image"},
		},
		{
			name: "write manifest error propagates",
			opts: Options{
				OutputDir:   "/test/out",
				Images:      []string{"nginx:1.27"},
				Concurrency: 2,
			},
			existingManifest: &pushspec.PushManifest{
				LayoutDir: "oci-layout",
				Images:    []pushspec.ArchiveSpec{},
			},
			archivedSpecs: []pushspec.ArchiveSpec{
				{Image: "nginx:1.27", Target: "library/nginx:1.27", OCIDigest: "sha256:abc"},
			},
			writeManifestErr:   fmt.Errorf("permission denied"),
			wantErr:            true,
			wantErrContains:    "write updated push manifest",
			wantArchivedImages: []string{"nginx:1.27"},
			wantWrittenSpecs: []pushspec.ArchiveSpec{
				{Image: "nginx:1.27", Target: "library/nginx:1.27", OCIDigest: "sha256:abc"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedArchivedImages []string
			var capturedWrittenSpecs []pushspec.ArchiveSpec
			var statusBuf bytes.Buffer

			r := Runner{
				readManifest: func(inputDir string) (*pushspec.PushManifest, error) {
					if tt.readManifestErr != nil {
						return nil, tt.readManifestErr
					}
					return tt.existingManifest, nil
				},
				archiveImages: func(ctx context.Context, images []string, outputDir string, concurrency int, status ...io.Writer) ([]pushspec.ArchiveSpec, error) {
					capturedArchivedImages = images
					if tt.archiveImagesErr != nil {
						return nil, tt.archiveImagesErr
					}
					return tt.archivedSpecs, nil
				},
				writeManifest: func(outputDir string, specs []pushspec.ArchiveSpec) error {
					capturedWrittenSpecs = specs
					if tt.writeManifestErr != nil {
						return tt.writeManifestErr
					}
					return nil
				},
			}

			err := r.Run(context.Background(), tt.opts, &statusBuf)

			if (err != nil) != tt.wantErr {
				t.Errorf("Run() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErrContains != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErrContains)) {
				t.Errorf("Run() error = %v, want to contain %q", err, tt.wantErrContains)
			}

			if tt.wantArchivedImages != nil {
				if !equalStringSlices(capturedArchivedImages, tt.wantArchivedImages) {
					t.Errorf("archived images = %v, want %v", capturedArchivedImages, tt.wantArchivedImages)
				}
			}

			if tt.wantWrittenSpecs != nil {
				if !equalArchiveSpecs(capturedWrittenSpecs, tt.wantWrittenSpecs) {
					t.Errorf("written specs = %v, want %v", capturedWrittenSpecs, tt.wantWrittenSpecs)
				}
			}

			if tt.wantStatusContains != "" {
				statusText := statusBuf.String()
				if !strings.Contains(statusText, tt.wantStatusContains) {
					t.Errorf("status = %q, want to contain %q", statusText, tt.wantStatusContains)
				}
			}
		})
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalArchiveSpecs(a, b []pushspec.ArchiveSpec) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Image != b[i].Image || a[i].Target != b[i].Target || a[i].OCIDigest != b[i].OCIDigest || a[i].Optional != b[i].Optional || !equalStringSlices(a[i].OptionalFlags, b[i].OptionalFlags) {
			return false
		}
	}
	return true
}
