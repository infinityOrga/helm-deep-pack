# Pull inventory benchmark

This comparison measures the normal Helm render before and after the optional
image inventory changes. The changed tree also reports the cost of its normal
render plus synthetic discovery and default (15-second budget) attribution;
the baseline has no equivalent feature path.

## Setup

- Date: 2026-09-18
- Chart: `prometheus-node-exporter` 4.57.0
- Chart source: [prometheus-community/helm-charts release](https://github.com/prometheus-community/helm-charts/releases/download/prometheus-node-exporter-4.57.0/prometheus-node-exporter-4.57.0.tgz)
- Host: Linux `7.0.0-1006-aws`, Intel Xeon Platinum 8488C, 2 CPUs
- Go: `go1.26.3 linux/amd64`
- Build mode: `CGO_ENABLED=0`
- Benchmark flags: `-benchmem -benchtime=1s -count=5`

The archive was unpacked into an unpacked chart directory and supplied through
`HELM_DEEP_PACK_BENCH_CHART`. The system `tar` could not create files in this
environment, so Python's standard-library `tarfile` extractor was used.

`kube-prometheus-stack` 91.4.1 and Prometheus 29.30.2 were also considered,
but their synthetic renders failed with chart-specific errors before a stable
comparison could be collected: the former required
`grafana.operator.matchLabels` when
`grafana.operator.dashboardsConfigMapRefEnabled` was true, and the latter had
a YAML parse error in `prometheus/templates/vpa.yaml`. The smaller
`prometheus-node-exporter` chart was therefore used as the reproducible
Prometheus-community benchmark.

## Commands

Changed tree:

```bash
HELM_DEEP_PACK_BENCH_CHART=/path/to/prometheus-node-exporter \
CGO_ENABLED=0 go test ./internal/pull -run '^$' \
  -bench '^BenchmarkNodeExporter(NormalRender|NormalAndSyntheticDiscovery)$' \
  -benchmem -benchtime=1s -count=5
```

Baseline tree (`9b1b12e`): the same benchmark harness was copied into the
baseline worktree without changing production code, and only the normal-render
benchmark was run because the baseline has no synthetic discovery benchmark.

```bash
HELM_DEEP_PACK_BENCH_CHART=/path/to/prometheus-node-exporter \
CGO_ENABLED=0 go test ./internal/pull -run '^$' \
  -bench '^BenchmarkNodeExporterNormalRender$' \
  -benchmem -benchtime=1s -count=5
```

## Results

Values below are the median of the five samples from each command.

| Tree / workload | Time | Bytes/op | Allocs/op |
| --- | ---: | ---: | ---: |
| Baseline normal render | 2.931 ms | 1,153,515 | 19,628 |
| Changed normal render | 2.949 ms | 1,154,350 | 19,637 |
| Changed normal + synthetic discovery + attribution | 92.269 ms | 34,952,493 | 651,467 |

The normal-render path stayed within benchmark noise: the changed median was
about 0.6% higher in time, with approximately 0.1% more bytes and 9 more
allocations per operation. The full default inventory path is intentionally
more expensive because it performs additional Helm renders to discover images
behind disabled boolean values and attribute their flag paths.
