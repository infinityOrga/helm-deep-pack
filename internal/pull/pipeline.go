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

type imageInventoryEntry struct {
	Image         string
	Optional      bool
	OptionalFlags []string
}

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

	optionalDiscovery, discoveryErr := r.discoverOptionalImages(runCtx, opts, images)
	if discoveryErr != nil {
		if err := writeStatus(statusOut, "warning: optional image discovery failed; optional images may be missing: %v\n", discoveryErr); err != nil {
			return PullResult{}, err
		}
	} else {
		for _, warning := range optionalDiscovery.Warnings {
			if err := writeStatus(statusOut, "warning: %s\n", warning); err != nil {
				return PullResult{}, err
			}
		}
	}

	inventory := mergeImageInventory(chartImages, images, optionalDiscovery)
	requiredImages, optionalImages := splitImageInventory(inventory)

	specs := make([]pushspec.ArchiveSpec, 0, len(inventory))
	if len(requiredImages) > 0 {
		archived, archiveErr := r.archiveImages(runCtx, requiredImages, outputDir, opts.Concurrency, statusOut)
		if archiveErr != nil {
			cancel()
			return PullResult{}, archiveErr
		}
		applyImageMetadata(archived, inventory)
		specs = append(specs, archived...)
	}
	if len(optionalImages) > 0 {
		archived, failures, archiveErr := r.archiveOptionalImages(runCtx, optionalImages, outputDir, opts.Concurrency, statusOut)
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
		applyImageMetadata(archived, inventory)
		specs = append(specs, archived...)
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

func writeStatus(w io.Writer, format string, args ...interface{}) error {
	if _, err := fmt.Fprintf(w, format, args...); err != nil {
		return fmt.Errorf("write status: %w", err)
	}
	return nil
}

func mergeImageInventory(chartImages, renderedImages []string, optional optionalImageDiscovery) []imageInventoryEntry {
	entries := make([]imageInventoryEntry, 0, len(chartImages)+len(renderedImages)+len(optional.Images))
	byImage := make(map[string]int, cap(entries))
	add := func(image string, isOptional bool, flags []string) {
		if index, ok := byImage[image]; ok {
			entry := &entries[index]
			if !isOptional {
				entry.Optional = false
				entry.OptionalFlags = nil
				return
			}
			if entry.Optional {
				entry.OptionalFlags = appendUnique(entry.OptionalFlags, flags...)
			}
			return
		}
		byImage[image] = len(entries)
		entries = append(entries, imageInventoryEntry{
			Image:         image,
			Optional:      isOptional,
			OptionalFlags: append([]string(nil), flags...),
		})
	}

	for _, image := range chartImages {
		add(image, true, nil)
	}
	for _, image := range renderedImages {
		add(image, false, nil)
	}
	for _, image := range optional.Images {
		add(image, true, optional.OptionalFlags[image])
	}
	return entries
}

func splitImageInventory(entries []imageInventoryEntry) (required, optional []string) {
	for _, entry := range entries {
		if entry.Optional {
			optional = append(optional, entry.Image)
			continue
		}
		required = append(required, entry.Image)
	}
	return required, optional
}

func applyImageMetadata(specs []pushspec.ArchiveSpec, entries []imageInventoryEntry) {
	metadata := make(map[string]imageInventoryEntry, len(entries))
	for _, entry := range entries {
		metadata[entry.Image] = entry
	}
	for index := range specs {
		entry, ok := metadata[specs[index].Image]
		if !ok {
			continue
		}
		specs[index].Optional = entry.Optional
		if entry.Optional {
			specs[index].OptionalFlags = append([]string(nil), entry.OptionalFlags...)
		} else {
			specs[index].OptionalFlags = nil
		}
	}
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
