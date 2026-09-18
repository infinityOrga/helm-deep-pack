package pull

import (
	"context"
	"os"
	"testing"
	"time"

	"helm-deep-pack/internal/chartimages"
)

// These benchmarks run against unpacked real charts. They are skipped during
// ordinary test runs so the unit suite never requires a network download.
// Set the chart-specific environment variables documented in BENCHMARK.md.
func BenchmarkNodeExporterNormalRender(b *testing.B) {
	benchmarkNormalRender(b, benchmarkChart{
		name:   "prometheus-node-exporter",
		envVar: "HELM_DEEP_PACK_BENCH_NODE_EXPORTER",
		legacy: "HELM_DEEP_PACK_BENCH_CHART",
	})
}

func BenchmarkNodeExporterNormalAndSyntheticDiscovery(b *testing.B) {
	benchmarkNormalAndSyntheticDiscovery(b, benchmarkChart{
		name:   "prometheus-node-exporter",
		envVar: "HELM_DEEP_PACK_BENCH_NODE_EXPORTER",
		legacy: "HELM_DEEP_PACK_BENCH_CHART",
	})
}

func BenchmarkNodeExporterNormalAndSyntheticDiscoveryNoAttribution(b *testing.B) {
	benchmarkNormalAndSyntheticDiscoveryNoAttribution(b, benchmarkChart{
		name:   "prometheus-node-exporter",
		envVar: "HELM_DEEP_PACK_BENCH_NODE_EXPORTER",
		legacy: "HELM_DEEP_PACK_BENCH_CHART",
	})
}

func BenchmarkKubePrometheusStackNormalRender(b *testing.B) {
	benchmarkNormalRender(b, benchmarkChart{
		name:   "kube-prometheus-stack",
		envVar: "HELM_DEEP_PACK_BENCH_KUBE_PROMETHEUS_STACK",
	})
}

func BenchmarkKubePrometheusStackNormalAndSyntheticDiscovery(b *testing.B) {
	benchmarkNormalAndSyntheticDiscovery(b, benchmarkChart{
		name:   "kube-prometheus-stack",
		envVar: "HELM_DEEP_PACK_BENCH_KUBE_PROMETHEUS_STACK",
	})
}

func BenchmarkKubePrometheusStackNormalAndSyntheticDiscoveryNoAttribution(b *testing.B) {
	benchmarkNormalAndSyntheticDiscoveryNoAttribution(b, benchmarkChart{
		name:   "kube-prometheus-stack",
		envVar: "HELM_DEEP_PACK_BENCH_KUBE_PROMETHEUS_STACK",
	})
}

func BenchmarkPrometheusNormalRender(b *testing.B) {
	benchmarkNormalRender(b, benchmarkChart{
		name:   "prometheus",
		envVar: "HELM_DEEP_PACK_BENCH_PROMETHEUS",
	})
}

func BenchmarkPrometheusNormalAndSyntheticDiscovery(b *testing.B) {
	benchmarkNormalAndSyntheticDiscovery(b, benchmarkChart{
		name:   "prometheus",
		envVar: "HELM_DEEP_PACK_BENCH_PROMETHEUS",
	})
}

func BenchmarkPrometheusNormalAndSyntheticDiscoveryNoAttribution(b *testing.B) {
	benchmarkNormalAndSyntheticDiscoveryNoAttribution(b, benchmarkChart{
		name:   "prometheus",
		envVar: "HELM_DEEP_PACK_BENCH_PROMETHEUS",
	})
}

type benchmarkChart struct {
	name   string
	envVar string
	legacy string
}

func benchmarkNormalRender(b *testing.B, chart benchmarkChart) {
	b.Helper()
	chartDir := benchmarkChartDir(b, chart)
	runner := NewRunner()
	opts := Options{Chart: chartDir, RenderedOnly: true}

	if _, err := runner.renderChartManifest(context.Background(), opts); err != nil {
		b.Fatalf("%s warm normal render: %v", chart.name, err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := runner.renderChartManifest(context.Background(), opts); err != nil {
			b.Fatalf("%s normal render: %v", chart.name, err)
		}
	}
}

func benchmarkNormalAndSyntheticDiscovery(b *testing.B, chart benchmarkChart) {
	benchmarkNormalAndSyntheticDiscoveryWithTimeout(b, chart, DefaultOptionalImageTimeout)
}

func benchmarkNormalAndSyntheticDiscoveryNoAttribution(b *testing.B, chart benchmarkChart) {
	benchmarkNormalAndSyntheticDiscoveryWithTimeout(b, chart, 0)
}

func benchmarkNormalAndSyntheticDiscoveryWithTimeout(b *testing.B, chart benchmarkChart, timeout time.Duration) {
	b.Helper()
	chartDir := benchmarkChartDir(b, chart)
	runner := NewRunner()
	opts := Options{Chart: chartDir, OptionalImageTimeout: timeout}

	warmNormal, err := runner.renderChartManifest(context.Background(), opts)
	if err != nil {
		b.Fatalf("%s warm normal render: %v", chart.name, err)
	}
	warmBaseline, err := chartimages.ExtractImages(warmNormal)
	if err != nil {
		b.Fatalf("%s extract warm normal images: %v", chart.name, err)
	}
	if discovery, err := runner.discoverOptionalChartImages(context.Background(), opts, warmBaseline); err != nil {
		b.Fatalf("%s warm synthetic discovery: %v", chart.name, err)
	} else {
		b.Logf("%s warm inventory: required=%d optional=%d warnings=%d", chart.name, len(warmBaseline), len(discovery.Images), len(discovery.Warnings))
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		normal, err := runner.renderChartManifest(context.Background(), opts)
		if err != nil {
			b.Fatalf("%s normal render: %v", chart.name, err)
		}
		baseline, err := chartimages.ExtractImages(normal)
		if err != nil {
			b.Fatalf("%s extract normal images: %v", chart.name, err)
		}
		if discovery, err := runner.discoverOptionalChartImages(context.Background(), opts, baseline); err != nil {
			b.Fatalf("%s synthetic discovery: %v", chart.name, err)
		} else if len(discovery.Warnings) > 0 {
			b.Logf("%s iteration %d warnings: %v", chart.name, i, discovery.Warnings)
		}
	}
}

func benchmarkChartDir(b *testing.B, chart benchmarkChart) string {
	b.Helper()
	chartDir := os.Getenv(chart.envVar)
	if chartDir == "" && chart.legacy != "" {
		chartDir = os.Getenv(chart.legacy)
	}
	if chartDir == "" {
		b.Skipf("set %s to an unpacked %s chart", chart.envVar, chart.name)
	}
	info, err := os.Stat(chartDir)
	if err != nil {
		b.Fatalf("stat %s benchmark chart: %v", chart.name, err)
	}
	if !info.IsDir() {
		b.Fatalf("%s benchmark chart %q is not a directory", chart.name, chartDir)
	}
	return chartDir
}
