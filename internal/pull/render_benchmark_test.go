package pull

import (
	"context"
	"os"
	"testing"

	"helm-deep-pack/internal/chartimages"
)

// Run these benchmarks against a real chart by setting HELM_DEEP_PACK_BENCH_CHART
// to an unpacked chart directory. They are skipped during ordinary test runs so
// the unit suite never requires a network download.
func BenchmarkNodeExporterNormalRender(b *testing.B) {
	chartDir := benchmarkChartDir(b)
	runner := NewRunner()
	opts := Options{Chart: chartDir, RenderedOnly: true}

	if _, err := runner.renderChartManifest(context.Background(), opts); err != nil {
		b.Fatalf("warm normal render: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := runner.renderChartManifest(context.Background(), opts); err != nil {
			b.Fatalf("normal render: %v", err)
		}
	}
}

func BenchmarkNodeExporterNormalAndSyntheticDiscovery(b *testing.B) {
	chartDir := benchmarkChartDir(b)
	runner := NewRunner()
	opts := Options{Chart: chartDir, OptionalImageTimeout: DefaultOptionalImageTimeout}

	normal, err := runner.renderChartManifest(context.Background(), opts)
	if err != nil {
		b.Fatalf("warm normal render: %v", err)
	}
	baseline, err := chartimages.ExtractImages(normal)
	if err != nil {
		b.Fatalf("extract warm normal images: %v", err)
	}
	if _, err := runner.discoverOptionalChartImages(context.Background(), opts, baseline); err != nil {
		b.Fatalf("warm synthetic discovery: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		normal, err := runner.renderChartManifest(context.Background(), opts)
		if err != nil {
			b.Fatalf("normal render: %v", err)
		}
		baseline, err := chartimages.ExtractImages(normal)
		if err != nil {
			b.Fatalf("extract normal images: %v", err)
		}
		if _, err := runner.discoverOptionalChartImages(context.Background(), opts, baseline); err != nil {
			b.Fatalf("synthetic discovery: %v", err)
		}
	}
}

func benchmarkChartDir(b *testing.B) string {
	b.Helper()
	chartDir := os.Getenv("HELM_DEEP_PACK_BENCH_CHART")
	if chartDir == "" {
		b.Skip("set HELM_DEEP_PACK_BENCH_CHART to an unpacked chart directory")
	}
	info, err := os.Stat(chartDir)
	if err != nil {
		b.Fatalf("stat benchmark chart: %v", err)
	}
	if !info.IsDir() {
		b.Fatalf("benchmark chart %q is not a directory", chartDir)
	}
	return chartDir
}
