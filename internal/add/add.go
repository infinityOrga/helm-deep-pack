// Package add augments existing pull output directories by staging additional
// container images into the existing OCI layout and updating the push manifest.
//
// The package mirrors the seam pattern from internal/pull: Runner's function-field
// collaborators are wired in NewRunner and substituted in tests to drive the
// workflow without network calls.
package add

import (
	"context"
	"fmt"
	"helm-deep-pack/internal/progress"
	"io"
	"os"

	"helm-deep-pack/internal/push"
	"helm-deep-pack/internal/pushspec"
)

type Options struct {
	OutputDir   string
	Images      []string
	Concurrency int
}

type Runner struct {
	archiveImages func(ctx context.Context, images []string, outputDir string, concurrency int, status ...io.Writer) ([]pushspec.ArchiveSpec, error)
	readManifest  func(inputDir string) (*pushspec.PushManifest, error)
	writeManifest func(outputDir string, specs []pushspec.ArchiveSpec) error
}

func Run(ctx context.Context, opts Options, status ...io.Writer) error {
	return NewRunner().Run(ctx, opts, status...)
}

func NewRunner() Runner {
	return Runner{
		archiveImages: push.ArchiveImages,
		readManifest:  pushspec.ReadPushManifest,
		writeManifest: pushspec.WritePushManifest,
	}
}

func (r Runner) Run(ctx context.Context, opts Options, status ...io.Writer) error {
	statusOut := progress.StatusWriter(status...)

	outputDir := opts.OutputDir
	if outputDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("get current directory: %w", err)
		}
		outputDir = cwd
	}

	manifest, err := r.readManifest(outputDir)
	if err != nil {
		return fmt.Errorf("read push manifest (run pull first to create output directory): %w", err)
	}

	newImages := dedupeImages(manifest, opts.Images)
	if len(newImages) == 0 {
		_, _ = fmt.Fprintln(statusOut, "all images already present; nothing to add")
		return nil
	}

	newSpecs, err := r.archiveImages(ctx, newImages, outputDir, opts.Concurrency, statusOut)
	if err != nil {
		return fmt.Errorf("archive new images: %w", err)
	}
	for index := range newSpecs {
		// Images supplied explicitly through add are required, regardless of
		// metadata returned by an archive collaborator.
		newSpecs[index].Optional = false
		newSpecs[index].OptionalFlags = nil
	}

	mergedSpecs := append(manifest.Images, newSpecs...)
	if err := r.writeManifest(outputDir, mergedSpecs); err != nil {
		return fmt.Errorf("write updated push manifest: %w", err)
	}

	_, _ = fmt.Fprintf(statusOut, "added %d image(s)\n", len(newSpecs))
	return nil
}

func dedupeImages(manifest *pushspec.PushManifest, incoming []string) []string {
	existing := make(map[string]bool, len(manifest.Images))
	for _, spec := range manifest.Images {
		existing[spec.Image] = true
	}

	seen := make(map[string]bool)
	result := make([]string, 0, len(incoming))
	for _, image := range incoming {
		if existing[image] || seen[image] {
			continue
		}
		seen[image] = true
		result = append(result, image)
	}
	return result
}
