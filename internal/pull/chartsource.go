package pull

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	helmchart "helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/getter"
	"helm.sh/helm/v3/pkg/repo"

	"helm-deep-pack/internal/chartimages"
)

const maxChartArchiveBytes = 16 << 20 // 16 MiB

func (r Runner) extractChartAnnotationImages(ctx context.Context, opts Options) ([]string, error) {
	if opts.Chart == "" {
		return nil, nil
	}

	loaded, err := r.loadChart(ctx, opts)
	if err != nil {
		return nil, err
	}
	return extractChartAnnotationImagesRecursive(loaded.Chart)
}

func extractChartAnnotationImagesRecursive(chrt *helmchart.Chart) ([]string, error) {
	// This adapter keeps traversal in the pull package while leaving annotation
	// payload parsing and image validation in chartimages.
	var images []string
	var visit func(*helmchart.Chart) error
	visit = func(current *helmchart.Chart) error {
		if current == nil {
			return nil
		}
		if current.Metadata != nil {
			found, err := chartimages.ExtractChartAnnotationImages(current.Metadata.Annotations)
			if err != nil {
				return err
			}
			images = appendUnique(images, found...)
		}
		for _, dependency := range current.Dependencies() {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		return nil
	}

	if err := visit(chrt); err != nil {
		return nil, err
	}
	return images, nil
}

func (r Runner) loadChart(ctx context.Context, opts Options) (loadedChart, error) {
	if cached := r.getCachedChart(opts); cached != nil {
		return *cached, nil
	}

	if isOCIRef(opts.Chart) || isOCIRef(opts.Repo) {
		chrt, err := r.ociChartSource(ctx, opts)
		if err != nil {
			return loadedChart{}, err
		}
		r.setCachedChart(opts, chrt)
		return chrt, nil
	}

	if opts.Repo == "" {
		chrt, localErr := r.localChartSource(ctx, opts)
		if localErr == nil {
			r.setCachedChart(opts, chrt)
			return chrt, nil
		}

		// Fallback to configured repos if local load fails
		chrt, fallbackErr := r.loadChartFromConfiguredRepos(ctx, opts)
		if fallbackErr == nil {
			r.setCachedChart(opts, chrt)
			return chrt, nil
		}

		if _, ok := errors.AsType[*repoLookupError](fallbackErr); ok {
			return loadedChart{}, fallbackErr
		}

		return loadedChart{}, fmt.Errorf(
			"chart %q not found. download/pull the chart first or add its repository to Helm",
			opts.Chart,
		)
	}
	chrt, err := r.helmChartSource(ctx, opts)
	if err != nil {
		return loadedChart{}, err
	}
	r.setCachedChart(opts, chrt)
	return chrt, nil
}

func (r Runner) getCachedChart(opts Options) *loadedChart {
	if r.chartCache == nil {
		return nil
	}
	key := cacheKeyForOptions(opts)
	r.chartCache.mu.Lock()
	defer r.chartCache.mu.Unlock()
	cached, ok := r.chartCache.byOpts[key]
	if !ok {
		return nil
	}
	return cached
}

func (r Runner) setCachedChart(opts Options, chrt loadedChart) {
	if r.chartCache == nil || chrt.Chart == nil {
		return
	}
	key := cacheKeyForOptions(opts)
	r.chartCache.mu.Lock()
	r.chartCache.byOpts[key] = &chrt
	r.chartCache.mu.Unlock()
}

func cacheKeyForOptions(opts Options) string {
	return strings.Join([]string{
		opts.Repo,
		opts.Chart,
		opts.Version,
		strings.Join(opts.ValuesFiles, "\x1f"),
		strings.Join(opts.SetValues, "\x1f"),
	}, "\x00")
}

func localChartSource(_ context.Context, opts Options) (loadedChart, error) {
	chrt, err := loader.Load(opts.Chart)
	if err != nil {
		return loadedChart{}, err
	}
	localArchivePath := ""
	if info, statErr := os.Stat(opts.Chart); statErr == nil && !info.IsDir() && isChartArchivePath(opts.Chart) {
		localArchivePath = opts.Chart
	}
	return loadedChart{
		Chart:            chrt,
		LocalArchivePath: localArchivePath,
		Info: ChartInfo{
			Name:    chrt.Metadata.Name,
			Version: chrt.Metadata.Version,
			Source:  opts.Chart,
		},
	}, nil
}

func loadHelmRepoChart(ctx context.Context, opts Options) (loadedChart, error) {
	index, err := loadRepoIndex(ctx, opts.Repo)
	if err != nil {
		return loadedChart{}, err
	}

	versions, ok := index[opts.Chart]
	if !ok || len(versions) == 0 {
		return loadedChart{}, &repoLookupError{
			kind:   repoLookupKindMissingChart,
			repo:   opts.Repo,
			chart:  opts.Chart,
			charts: availableChartNames(index),
		}
	}

	version := opts.Version
	if version == "" {
		version, err = selectStableVersion(versions)
		if err != nil {
			return loadedChart{}, fmt.Errorf(
				"no stable version found for %s/%s",
				opts.Repo, opts.Chart,
			)
		}
	} else if !versionExists(versions, version) {
		return loadedChart{}, &repoLookupError{
			kind:         repoLookupKindVersionMissing,
			repo:         opts.Repo,
			chart:        opts.Chart,
			requestedVer: version,
			versions:     availableStableVersions(versions),
		}
	}

	chartURL, err := repo.FindChartInRepoURL(opts.Repo, opts.Chart, version, "", "", "", getter.All(cli.New()))
	if err != nil {
		return loadedChart{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, chartURL, nil)
	if err != nil {
		return loadedChart{}, fmt.Errorf("prepare chart archive request: %w", err)
	}
	resp, err := outboundHTTPClient.Do(req)
	if err != nil {
		return loadedChart{}, fmt.Errorf("fetch chart archive: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return loadedChart{}, fmt.Errorf("fetch chart archive: %s: %s", resp.Status, string(body))
	}

	archiveData, err := readChartArchiveData(resp.Body)
	if err != nil {
		return loadedChart{}, err
	}

	chrt, err := loader.LoadArchive(bytes.NewReader(archiveData))
	if err != nil {
		return loadedChart{}, err
	}
	return loadedChart{
		Chart:       chrt,
		ArchiveData: archiveData,
		ArchiveName: chartArchiveNameFromURL(chartURL),
		Info: ChartInfo{
			Name:    chrt.Metadata.Name,
			Version: chrt.Metadata.Version,
			Source:  opts.Repo,
		},
	}, nil
}

func readChartArchiveData(r io.Reader) ([]byte, error) {
	limited := io.LimitReader(r, maxChartArchiveBytes+1)
	archiveData, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read chart archive response: %w", err)
	}
	if int64(len(archiveData)) > maxChartArchiveBytes {
		return nil, fmt.Errorf("chart archive exceeds size limit (%d bytes)", maxChartArchiveBytes)
	}
	return archiveData, nil
}

func (r Runner) loadChartFromConfiguredRepos(ctx context.Context, opts Options) (loadedChart, error) {
	configRepos, err := loadConfiguredRepos()
	if err != nil {
		return loadedChart{}, fmt.Errorf("read helm repositories config: %w", err)
	}

	if len(configRepos) == 0 {
		return loadedChart{}, fmt.Errorf("no configured helm repositories found")
	}

	var versionErr *repoLookupError
	availableCharts := make(map[string]struct{})
	var otherErrs []string
	for _, configRepo := range configRepos {
		chrt, err := loadHelmRepoChart(ctx, Options{
			Chart:   opts.Chart,
			Repo:    configRepo.URL,
			Version: opts.Version,
		})

		if err == nil {
			return chrt, nil
		}

		var lookupErr *repoLookupError
		if errors.As(err, &lookupErr) {
			if lookupErr.kind == repoLookupKindVersionMissing {
				if versionErr == nil {
					versionErr = lookupErr
				}
				continue
			}
			for _, name := range lookupErr.charts {
				availableCharts[name] = struct{}{}
			}
			continue
		}

		otherErrs = append(otherErrs, fmt.Sprintf("%s: %v", configRepo.Name, err))
	}

	if versionErr != nil {
		return loadedChart{}, versionErr
	}

	if len(availableCharts) > 0 {
		return loadedChart{}, &repoLookupError{
			kind:   repoLookupKindMissingChart,
			repo:   "configured repos",
			chart:  opts.Chart,
			charts: sortedKeys(availableCharts),
		}
	}

	if len(otherErrs) > 0 {
		return loadedChart{}, fmt.Errorf("search configured repos: %s", strings.Join(otherErrs, "; "))
	}

	return loadedChart{}, fmt.Errorf("search configured repos: no chart match for %q", opts.Chart)
}

func loadConfiguredRepos() ([]*repo.Entry, error) {
	settings := cli.New()
	repoFile := settings.RepositoryConfig

	repoIndex, err := repo.LoadFile(repoFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		// Return actual error so caller can distinguish between config issues
		// and legitimately no configured repos
		return nil, err
	}

	if len(repoIndex.Repositories) == 0 {
		// No configured repos is not an error condition
		return nil, nil
	}

	return repoIndex.Repositories, nil
}
