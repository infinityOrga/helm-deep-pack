package pull

import (
	"context"
	"fmt"
	"helm-deep-pack/internal/progress"
	"helm-deep-pack/internal/pushspec"
	"io"
	"os"
	"path/filepath"
)

func (r Runner) Run(ctx context.Context, opts Options, status ...io.Writer) error {
	_, err := r.Execute(ctx, opts, status...)
	if err != nil {
		return fmt.Errorf("execute pull: %w", err)
	}
	return nil
}

func (r Runner) Execute(ctx context.Context, opts Options, status ...io.Writer) (result PullResult, err error) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	statusOut := progress.StatusWriter(status...)
	outputDir := opts.OutputDir
	defer func() {
		if err == nil || outputDir == "" {
			return
		}
		if entries, readErr := os.ReadDir(outputDir); readErr == nil && len(entries) == 0 {
			_ = os.Remove(outputDir)
		}
	}()

	if outputDir == "" {
		dir, err := defaultOutputDir(opts.Chart)
		if err != nil {
			return PullResult{}, fmt.Errorf("default output dir: %w", err)
		}
		outputDir = dir
	} else if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return PullResult{}, fmt.Errorf("create output dir: %w", err)
	}

	loaded, err := r.loadChart(runCtx, opts)
	if err != nil {
		return PullResult{}, fmt.Errorf("load chart: %w", err)
	}
	_, _ = fmt.Fprintf(statusOut, "chart: name=%s version=%s source=%s\n", loaded.Info.Name, loaded.Info.Version, loaded.Info.Source)

	chartImages, err := r.extractChartImages(runCtx, opts)
	if err != nil {
		return PullResult{}, fmt.Errorf("extract chart images: %w", err)
	}

	manifest, err := r.renderManifest(r, runCtx, opts)
	if err != nil {
		cancel()
		return PullResult{}, fmt.Errorf("render manifest: %w", err)
	}

	images, err := r.extractImages(manifest)
	if err != nil {
		cancel()
		return PullResult{}, fmt.Errorf("extract images: %w", err)
	}
	allImages := appendUnique(chartImages, images...)

	specs := make([]pushspec.ArchiveSpec, 0, len(allImages))
	if len(allImages) > 0 {
		archived, archiveErr := r.archiveImages(runCtx, allImages, outputDir, opts.Concurrency, statusOut)
		if archiveErr != nil {
			cancel()
			return PullResult{}, archiveErr
		}
		specs = append(specs, archived...)
	}

	if err := r.writePushManifest(outputDir, specs); err != nil {
		return PullResult{}, fmt.Errorf("write push manifest: %w", err)
	}

	if _, err := r.stageChartArchive(loaded, outputDir); err != nil {
		return PullResult{}, fmt.Errorf("stage chart archive: %w", err)
	}

	if _, err := r.stagePushBinary(runCtx, outputDir, opts.DestinationPlatform, opts.HelperVersion); err != nil {
		return PullResult{}, fmt.Errorf("stage push binary: %w", err)
	}

	result = PullResult{
		OutputDir:    outputDir,
		Chart:        loaded.Info,
		Images:       allImages,
		ArchiveSpecs: specs,
		ManifestPath: filepath.Join(outputDir, pushspec.PushManifestFileName()),
	}
	return result, nil
}
