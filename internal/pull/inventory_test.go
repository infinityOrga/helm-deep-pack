package pull

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	helmchart "helm.sh/helm/v3/pkg/chart"
)

func TestDiscoverOptionalChartImagesFindsDisabledImage(t *testing.T) {
	chartDir := writeInventoryTestChart(t, `image:
  tag: v1
sidecar:
  enabled: false
  tag: v2
`, `apiVersion: apps/v1
kind: Deployment
metadata:
  name: example
spec:
  template:
    spec:
      containers:
        - name: app
          image: quay.io/example/app:{{ .Values.image.tag }}
        {{- if .Values.sidecar.enabled }}
        - name: sidecar
          image: quay.io/example/sidecar:{{ .Values.sidecar.tag }}
        {{- end }}
`)

	runner := NewRunner()
	discovery, err := runner.discoverOptionalChartImages(context.Background(), Options{
		Chart:                chartDir,
		OptionalImageTimeout: time.Second,
	}, []string{"quay.io/example/app:v1"})
	if err != nil {
		t.Fatalf("discoverOptionalChartImages() error = %v", err)
	}

	if got, want := discovery.Images, []string{"quay.io/example/sidecar:v2"}; !equalStrings(got, want) {
		t.Fatalf("optional images = %v, want %v", got, want)
	}
	if got, want := discovery.OptionalFlags["quay.io/example/sidecar:v2"], []string{".Values.sidecar.enabled"}; !equalStrings(got, want) {
		t.Fatalf("optional flags = %v, want %v", got, want)
	}

	normal, err := runner.renderChartManifest(context.Background(), Options{Chart: chartDir})
	if err != nil {
		t.Fatalf("renderChartManifest() after discovery error = %v", err)
	}
	if strings.Contains(normal, "quay.io/example/sidecar:v2") {
		t.Fatalf("normal render unexpectedly contains optional image: %q", normal)
	}
}

func TestDiscoverOptionalChartImagesSkipsSyntheticRenderWithoutFalseBooleans(t *testing.T) {
	chartDir := writeInventoryTestChart(t, `feature:
  enabled: true
`, `apiVersion: v1
kind: ConfigMap
metadata:
  name: example
data:
  image: quay.io/example/app:v1
`)

	runner := NewRunner()
	discovery, err := runner.discoverOptionalChartImages(context.Background(), Options{Chart: chartDir}, []string{"quay.io/example/app:v1"})
	if err != nil {
		t.Fatalf("discoverOptionalChartImages() error = %v", err)
	}
	if len(discovery.Images) != 0 || len(discovery.Warnings) != 0 {
		t.Fatalf("discovery = %#v, want empty discovery", discovery)
	}
}

func TestDiscoverOptionalChartImagesHonorsRenderedOnly(t *testing.T) {
	chartDir := writeInventoryTestChart(t, `feature:
  enabled: false
`, `apiVersion: v1
kind: ConfigMap
metadata:
  name: example
`)

	runner := NewRunner()
	discovery, err := runner.discoverOptionalChartImages(context.Background(), Options{
		Chart:        chartDir,
		RenderedOnly: true,
	}, nil)
	if err != nil {
		t.Fatalf("discoverOptionalChartImages() error = %v", err)
	}
	if len(discovery.Images) != 0 || len(discovery.Warnings) != 0 {
		t.Fatalf("discovery = %#v, want empty discovery", discovery)
	}
}

func TestDiscoverOptionalChartImagesAttributesConjunctionFlags(t *testing.T) {
	chartDir := writeInventoryTestChart(t, `first:
  enabled: false
second:
  enabled: false
`, `apiVersion: apps/v1
kind: Deployment
metadata:
  name: example
spec:
  template:
    spec:
      containers:
        - name: app
          image: quay.io/example/app:v1
        {{- if and .Values.first.enabled .Values.second.enabled }}
        - name: metrics
          image: quay.io/example/metrics:v1
        {{- end }}
`)

	discovery, err := NewRunner().discoverOptionalChartImages(context.Background(), Options{
		Chart:                chartDir,
		OptionalImageTimeout: time.Second,
	}, []string{"quay.io/example/app:v1"})
	if err != nil {
		t.Fatalf("discoverOptionalChartImages() error = %v", err)
	}
	if got, want := discovery.OptionalFlags["quay.io/example/metrics:v1"], []string{".Values.first.enabled", ".Values.second.enabled"}; !equalStrings(got, want) {
		t.Fatalf("conjunction flags = %v, want %v", got, want)
	}
}

func TestMinimalImageFlagSetOmitsPathsWhenProbeFails(t *testing.T) {
	paths := []valuePath{
		{{key: "first", isKey: true}},
		{{key: "second", isKey: true}},
	}

	got, complete := minimalImageFlagSet("quay.io/example/metrics:v1", paths, func([]valuePath) (map[string]struct{}, bool) {
		return nil, false
	}, time.Now().Add(time.Second))
	if complete {
		t.Fatal("minimalImageFlagSet() complete = true, want failed attribution")
	}
	if len(got) != 0 {
		t.Fatalf("minimalImageFlagSet() paths = %v, want no paths after probe failure", got)
	}
}

func TestMergeImageInventoryRequiredStatusWins(t *testing.T) {
	got := mergeImageInventory(
		[]string{"quay.io/example/annotation:v1", "quay.io/example/shared:v1"},
		[]string{"quay.io/example/shared:v1"},
		optionalImageDiscovery{
			Images: []string{"quay.io/example/optional:v1", "quay.io/example/shared:v1"},
			OptionalFlags: map[string][]string{
				"quay.io/example/optional:v1": {".Values.optional.enabled"},
				"quay.io/example/shared:v1":   {".Values.shared.enabled"},
			},
		},
	)

	want := []imageInventoryEntry{
		{Image: "quay.io/example/annotation:v1", Optional: true},
		{Image: "quay.io/example/shared:v1"},
		{Image: "quay.io/example/optional:v1", Optional: true, OptionalFlags: []string{".Values.optional.enabled"}},
	}
	if !equalImageInventory(got, want) {
		t.Fatalf("mergeImageInventory() = %#v, want %#v", got, want)
	}
}

func TestExtractChartAnnotationImagesRecursesThroughDependencies(t *testing.T) {
	root := &helmchart.Chart{Metadata: &helmchart.Metadata{Annotations: map[string]string{
		"annotation.helm.sh/images": "quay.io/example/root:v1",
	}}}
	child := &helmchart.Chart{Metadata: &helmchart.Metadata{Annotations: map[string]string{
		"annotation.helm.sh/images": "quay.io/example/child:v1",
	}}}
	grandchild := &helmchart.Chart{Metadata: &helmchart.Metadata{Annotations: map[string]string{
		"annotation.helm.sh/images": "quay.io/example/grandchild:v1",
	}}}
	child.AddDependency(grandchild)
	root.AddDependency(child)

	got, err := extractChartAnnotationImagesRecursive(root)
	if err != nil {
		t.Fatalf("extractChartAnnotationImagesRecursive() error = %v", err)
	}
	want := []string{
		"quay.io/example/root:v1",
		"quay.io/example/child:v1",
		"quay.io/example/grandchild:v1",
	}
	if !equalStrings(got, want) {
		t.Fatalf("recursive annotation images = %v, want %v", got, want)
	}
}

func writeInventoryTestChart(t *testing.T, values, template string) string {
	t.Helper()
	chartDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(chartDir, "templates"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartDir, "Chart.yaml"), []byte(`apiVersion: v2
name: inventory-test
version: 0.1.0
`), 0o644); err != nil {
		t.Fatalf("WriteFile(Chart.yaml) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartDir, "values.yaml"), []byte(values), 0o644); err != nil {
		t.Fatalf("WriteFile(values.yaml) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartDir, "templates", "deployment.yaml"), []byte(template), 0o644); err != nil {
		t.Fatalf("WriteFile(template) error = %v", err)
	}
	return chartDir
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range want {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

func equalImageInventory(got, want []imageInventoryEntry) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range want {
		if got[index].Image != want[index].Image || got[index].Optional != want[index].Optional || !equalStrings(got[index].OptionalFlags, want[index].OptionalFlags) {
			return false
		}
	}
	return true
}
