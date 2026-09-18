// Package pull renders Helm charts, extracts their referenced container
// images, and stages those images as OCI layout artifacts for later push.
//
// The package is organized around the Runner type, whose collaborators are
// wired in NewRunner. Responsibilities are split across files:
//
//   - runner.go      public API surface, core types, and Runner construction
//   - pipeline.go    pull workflow orchestration (Runner.Run / Runner.Execute)
//   - render.go      Helm chart manifest rendering
//   - chartsource.go chart loading, caching, and repository fallback
//   - repoindex.go   Helm repository index lookup and version resolution
//   - outputdir.go   output directory naming and creation
//   - slices.go      small slice/map utilities
package pull

import (
	"context"
	"io"
	"sync"
	"time"

	helmchart "helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chartutil"

	"helm-deep-pack/internal/chartimages"
	"helm-deep-pack/internal/push"
	"helm-deep-pack/internal/pushbin"
	"helm-deep-pack/internal/pushspec"
)

type Options struct {
	Chart                string
	Repo                 string
	Version              string
	OutputDir            string
	Concurrency          int
	ValuesFiles          []string
	SetValues            []string
	DestinationPlatform  string
	HelperVersion        string
	RenderedOnly         bool
	OptionalImageTimeout time.Duration
}

const DefaultOptionalImageTimeout = 15 * time.Second

type PullResult struct {
	OutputDir    string
	Chart        ChartInfo
	Images       []string
	ArchiveSpecs []pushspec.ArchiveSpec
	ManifestPath string
}

type ChartInfo struct {
	Name    string
	Version string
	Source  string
}

type loadedChart struct {
	Chart            *helmchart.Chart
	Info             ChartInfo
	LocalArchivePath string
	ArchiveData      []byte
	ArchiveName      string
}

type chartSourceAdapter func(ctx context.Context, opts Options) (loadedChart, error)

type Runner struct {
	searchRepoVersions     func(ctx context.Context, repo, chart string) ([]searchResult, error)
	renderManifest         func(r Runner, ctx context.Context, opts Options) (string, error)
	renderManifestValues   func(r Runner, ctx context.Context, opts Options, values map[string]interface{}) (string, error)
	lintRender             func(chrt *helmchart.Chart, values chartutil.Values) (map[string]string, []string, error)
	extractChartImages     func(ctx context.Context, opts Options) ([]string, error)
	extractImages          func(manifest string) ([]string, error)
	discoverOptionalImages func(ctx context.Context, opts Options, baseline []string) (optionalImageDiscovery, error)
	archiveImages          func(ctx context.Context, images []string, outputDir string, concurrency int, status ...io.Writer) ([]pushspec.ArchiveSpec, error)
	archiveOptionalImages  func(ctx context.Context, images []string, outputDir string, concurrency int, status ...io.Writer) ([]pushspec.ArchiveSpec, []push.ArchiveFailure, error)
	writePushManifest      func(outputDir string, specs []pushspec.ArchiveSpec) error
	stageChartArchive      func(loaded loadedChart, outputDir string) (string, error)
	stagePushBinary        func(ctx context.Context, outputDir, destinationPlatform, helperVersion string) (string, error)
	localChartSource       chartSourceAdapter
	helmChartSource        chartSourceAdapter
	ociChartSource         chartSourceAdapter
	chartCache             *loadedCharts
}

type loadedCharts struct {
	mu     sync.Mutex
	byOpts map[string]*loadedChart
}

func Run(ctx context.Context, opts Options, status ...io.Writer) error {
	return NewRunner().Run(ctx, opts, status...)
}

func NewRunner() Runner {
	lintRenderer := &helmLintRenderer{}
	r := Runner{
		searchRepoVersions: helmSearchRepoVersions,
		renderManifest: func(r Runner, ctx context.Context, opts Options) (string, error) {
			return r.renderChartManifest(ctx, opts)
		},
		renderManifestValues: func(r Runner, ctx context.Context, opts Options, values map[string]interface{}) (string, error) {
			return r.renderChartManifestWithValuesForDiscovery(ctx, opts, values)
		},
		lintRender:            lintRenderer.Render,
		extractImages:         chartimages.ExtractImages,
		archiveImages:         push.ArchiveImages,
		archiveOptionalImages: push.ArchiveImagesBestEffort,
		writePushManifest:     pushspec.WritePushManifest,
		stageChartArchive:     stageChartArchive,
		stagePushBinary:       pushbin.StageForPlatform,
		chartCache:            &loadedCharts{byOpts: make(map[string]*loadedChart)},
	}
	r.discoverOptionalImages = func(ctx context.Context, opts Options, baseline []string) (optionalImageDiscovery, error) {
		return r.discoverOptionalChartImages(ctx, opts, baseline)
	}
	r.extractChartImages = func(ctx context.Context, opts Options) ([]string, error) {
		return r.extractChartAnnotationImages(ctx, opts)
	}
	r.localChartSource = localChartSource
	r.helmChartSource = func(ctx context.Context, opts Options) (loadedChart, error) {
		return loadHelmRepoChart(ctx, opts)
	}
	r.ociChartSource = func(ctx context.Context, opts Options) (loadedChart, error) {
		return loadOCIChart(ctx, opts)
	}
	return r
}
