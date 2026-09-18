# Pull inventory benchmark

This benchmark compares normal Helm rendering with the optional-image inventory
work, using one Prometheus-community chart and two substantially larger charts.
The changed tree reports both synthetic discovery without attribution and the
complete default path, which includes the 15-second optional-flag attribution
budget.

## Setup

- Date: 2026-09-18
- Host: Linux `7.0.0-1006-aws`, Intel Xeon Platinum 8488C, 2 CPUs
- Go: `go1.26.3 linux/amd64`
- Build mode: `CGO_ENABLED=0`
- Benchmark flags: `-benchmem -benchtime=1s -count=5`
- Baseline commit: `9b1b12e`

Charts:

| Chart | Version | Source |
| --- | --- | --- |
| `prometheus-node-exporter` | 4.57.0 | [release archive](https://github.com/prometheus-community/helm-charts/releases/download/prometheus-node-exporter-4.57.0/prometheus-node-exporter-4.57.0.tgz) |
| `kube-prometheus-stack` | 91.4.1 | [release archive](https://github.com/prometheus-community/helm-charts/releases/download/kube-prometheus-stack-91.4.1/kube-prometheus-stack-91.4.1.tgz) |
| `prometheus` | 29.30.2 | [release archive](https://github.com/prometheus-community/helm-charts/releases/download/prometheus-29.30.2/prometheus-29.30.2.tgz) |

Each archive was unpacked into a chart directory. The environment variables
below point at those unpacked directories:

```bash
export HELM_DEEP_PACK_BENCH_NODE_EXPORTER=/path/to/prometheus-node-exporter
export HELM_DEEP_PACK_BENCH_KUBE_PROMETHEUS_STACK=/path/to/kube-prometheus-stack
export HELM_DEEP_PACK_BENCH_PROMETHEUS=/path/to/prometheus
```

The system `tar` could not create files in the original benchmark environment,
so Python's standard-library `tarfile` extractor was used. The chart archives
are not checked into the repository.

## Workarounds exercised

The synthetic render enables every effective false boolean, which intentionally
exercises chart branches that the default render does not. Two upstream chart
branches are not valid when all those booleans are enabled:

- `kube-prometheus-stack` 91.4.1 calls `fail` when synthetic discovery enables
  `grafana.operator.dashboardsConfigMapRefEnabled` without a selector. Discovery
  uses Helm lint mode for this inventory-only render, where chart-authored
  validation guards do not abort the render, and reports the guard messages as
  warnings. Its malformed synthetic
  `prometheus/serviceperreplica.yaml` file is then skipped independently, while
  the other rendered templates still contribute images.
- `prometheus` 29.30.2 produces malformed YAML in `templates/vpa.yaml` when
  synthetic values enable its VPA branch. The same per-template best-effort
  handling skips that file and retains images from valid templates.

The normal render remains strict. Both cases produce warnings during discovery;
they do not make the pull fail, and optional images remain marked optional.

## Commands

Changed tree, normal render for all three charts:

```bash
CGO_ENABLED=0 go test ./internal/pull -run '^$' \
  -bench '^Benchmark(NodeExporterNormalRender|KubePrometheusStackNormalRender|PrometheusNormalRender)$' \
  -benchmem -benchtime=1s -count=5
```

Changed tree, normal render plus synthetic discovery but with attribution
disabled. This isolates the cost of finding the discoverable image union:

```bash
CGO_ENABLED=0 go test ./internal/pull -run '^$' \
  -bench '^Benchmark(NodeExporterNormalAndSyntheticDiscoveryNoAttribution|KubePrometheusStackNormalAndSyntheticDiscoveryNoAttribution|PrometheusNormalAndSyntheticDiscoveryNoAttribution)$' \
  -benchmem -benchtime=1s -count=5
```

Changed tree, complete default path including attribution:

```bash
CGO_ENABLED=0 go test ./internal/pull -run '^$' \
  -bench '^Benchmark(NodeExporterNormalAndSyntheticDiscovery|KubePrometheusStackNormalAndSyntheticDiscovery|PrometheusNormalAndSyntheticDiscovery)$' \
  -benchmem -benchtime=1s -count=5
```

The full path is intentionally much slower for large charts: attribution is
best effort and bounded by `DefaultOptionalImageTimeout` (`15s`) plus a
32-render safety cap. Use the `NoAttribution` benchmark when measuring
render/discovery changes without that optional metadata work.

Baseline tree (`9b1b12e`): create a detached worktree and place a compatible
normal-render-only benchmark in `internal/pull/render_benchmark_test.go` (the
baseline predates `RenderedOnly` and synthetic-discovery seams). The compatible
harness should call `renderChartManifest` with each of the three chart paths and
use the same benchmark flags and environment variables. Then run:

```bash
CGO_ENABLED=0 go test ./internal/pull -run '^$' \
  -bench '^Benchmark(NodeExporterNormalRender|KubePrometheusStackNormalRender|PrometheusNormalRender)$' \
  -benchmem -benchtime=1s -count=5
```

## Results

Values below are medians of five samples. `ms` is milliseconds per benchmark
operation; bytes and allocations are per operation.

### Normal render: baseline versus changed

| Chart | Tree | Time | Bytes/op | Allocs/op |
| --- | --- | ---: | ---: | ---: |
| `prometheus-node-exporter` | baseline | 2.938 ms | 1,152,389 | 19,628 |
| `prometheus-node-exporter` | changed | 2.938 ms | 1,154,793 | 19,638 |
| `kube-prometheus-stack` | baseline | 143.019 ms | 78,030,679 | 727,437 |
| `kube-prometheus-stack` | changed | 140.682 ms | 77,463,190 | 704,506 |
| `prometheus` | baseline | 31.459 ms | 13,861,127 | 246,665 |
| `prometheus` | changed | 29.454 ms | 13,196,585 | 224,230 |

The normal render path did not regress. The changed implementation was within
noise for node-exporter and slightly lower in this run for the two larger
charts.

### Changed tree: normal render plus synthetic discovery, attribution disabled

| Chart | Time | Bytes/op | Allocs/op | Warm inventory |
| --- | ---: | ---: | ---: | --- |
| `prometheus-node-exporter` | 9.324 ms | 3,587,866 | 61,509 | 1 required, 3 optional |
| `kube-prometheus-stack` | 803.977 ms | 358,886,296 | 3,893,064 | 10 required, 8 optional |
| `prometheus` | 83.230 ms | 34,428,551 | 610,073 | 6 required, 5 optional |

### Changed tree: complete default path with attribution

| Chart | Time | Bytes/op | Allocs/op | Attribution result |
| --- | ---: | ---: | ---: | --- |
| `prometheus-node-exporter` | 45.121 ms | 17,040,839 | 315,393 | 3 optional, no warnings |
| `kube-prometheus-stack` | 9,274.929 ms | 4,311,313,592 | 48,230,851 | 8 optional; stopped at 32 probes |
| `prometheus` | 1,237.841 ms | 533,123,112 | 9,892,343 | 5 optional; stopped at 32 probes |

The large-chart attribution numbers are the important scaling result: the
optional image set is discovered in about 0.8s for kube-prometheus-stack and
83ms for Prometheus when attribution is disabled. The render-probe cap keeps
the default metadata pass below the full 15-second timeout and substantially
reduces allocation pressure, while timeout/cap/partial-probe warnings preserve
the optional marker when paths cannot be attributed.
