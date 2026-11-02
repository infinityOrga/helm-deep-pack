package pull

import (
	"context"
	"fmt"
	"helm-deep-pack/internal/progress"
	"helm-deep-pack/internal/pushspec"
	"io"
	"os"
	"path/filepath"
	"strings"
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
	if err := writeStatus(statusOut, "chart: name=%s version=%s source=%s\n", loaded.Info.Name, loaded.Info.Version, loaded.Info.Source); err != nil {
		return PullResult{}, err
	}

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

	var optionalDiscovery optionalImageDiscovery
	if !opts.NoOptional {
		var discoveryErr error
		optionalDiscovery, discoveryErr = r.discoverOptionalImages(runCtx, opts, images)
		if discoveryErr != nil {
			optionalDiscovery.Warnings = append(optionalDiscovery.Warnings, fmt.Sprintf("optional image discovery failed: %v", discoveryErr))
		}
		if err := writeOptionalImageDiagnostics(statusOut, loaded.Info.Name, optionalDiscovery, opts.Verbose); err != nil {
			return PullResult{}, err
		}
	}

	inventory := newImageInventory(chartImages, images, optionalDiscovery)
	specs := make([]pushspec.ArchiveSpec, 0, len(inventory.requiredImages)+len(inventory.optionalImages))
	appendArchivedSpecs := func(archived []pushspec.ArchiveSpec) {
		inventory.applyMetadata(archived)
		specs = append(specs, archived...)
	}
	if len(inventory.requiredImages) > 0 {
		archived, archiveErr := r.archiveImages(runCtx, inventory.requiredImages, outputDir, opts.Concurrency, statusOut)
		if archiveErr != nil {
			cancel()
			return PullResult{}, archiveErr
		}
		appendArchivedSpecs(archived)
	}
	if !opts.NoOptional && len(inventory.optionalImages) > 0 {
		archived, failures, archiveErr := r.archiveOptionalImages(runCtx, inventory.optionalImages, outputDir, opts.Concurrency, statusOut)
		if archiveErr != nil {
			if err := writeStatus(statusOut, "warning: optional image archiving failed; optional images may be missing: %v\n", archiveErr); err != nil {
				return PullResult{}, err
			}
		}
		for _, failure := range failures {
			if err := writeStatus(statusOut, "warning: skipping optional image %q: %v\n", failure.Image, failure.Err); err != nil {
				return PullResult{}, err
			}
		}
		appendArchivedSpecs(archived)
	}

	requiredCount, optionalCount := countImageCategories(specs)
	if err := writeStatus(statusOut, "images staged: required=%d optional=%d\n", requiredCount, optionalCount); err != nil {
		return PullResult{}, err
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
		Images:       archiveSpecImages(specs),
		ArchiveSpecs: specs,
		ManifestPath: filepath.Join(outputDir, pushspec.PushManifestFileName()),
	}
	return result, nil
}

func writeOptionalImageDiagnostics(status io.Writer, chartName string, discovery optionalImageDiscovery, verbose bool) error {
	var out strings.Builder
	if len(discovery.Warnings) > 0 {
		out.WriteString("warning: synthetic optional-image discovery was incomplete; optional images may be missing (selected render succeeded).\n")
		out.WriteString("  hint: enable wanted chart features with --set KEY=VALUE or -f values.yaml; --verbose shows details.\n")
		// ponytail: one verified chart recipe; add others only when verified.
		if chartName == "cilium" {
			out.WriteString("  hint: for Cilium Hubble, use --set hubble.enabled=true --set hubble.relay.enabled=true --set hubble.ui.enabled=true\n")
		}
	}
	if len(discovery.AttributionWarnings) > 0 {
		out.WriteString("note: optional image flag hints are incomplete; discovered images are retained. Use --optional-image-timeout 0 to skip attribution.\n")
	}
	if verbose {
		details := appendUnique(nil, discovery.Warnings...)
		details = appendUnique(details, discovery.AttributionWarnings...)
		for _, detail := range details {
			fmt.Fprintf(&out, "  detail: %s\n", strings.ReplaceAll(detail, "\n", "\n    "))
		}
	}
	if out.Len() == 0 {
		return nil
	}
	return writeStatus(status, "%s", out.String())
}

func writeStatus(w io.Writer, format string, args ...interface{}) error {
	if _, err := fmt.Fprintf(w, format, args...); err != nil {
		return fmt.Errorf("write status: %w", err)
	}
	return nil
}

func countImageCategories(specs []pushspec.ArchiveSpec) (required, optional int) {
	for _, spec := range specs {
		if spec.Optional {
			optional++
		} else {
			required++
		}
	}
	return required, optional
}

func archiveSpecImages(specs []pushspec.ArchiveSpec) []string {
	images := make([]string, 0, len(specs))
	for _, spec := range specs {
		images = append(images, spec.Image)
	}
	return images
}
