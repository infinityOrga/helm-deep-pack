package pull

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"helm-deep-pack/internal/pushspec"
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

	if got, want := discovery.Images, []string{"quay.io/example/sidecar:v2"}; !slices.Equal(got, want) {
		t.Fatalf("optional images = %v, want %v", got, want)
	}
	if got, want := discovery.OptionalFlags["quay.io/example/sidecar:v2"], []string{".Values.sidecar.enabled"}; !slices.Equal(got, want) {
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

func TestDiscoverOptionalChartImagesContinuesPastTemplateValidationFailure(t *testing.T) {
	chartDir := writeInventoryTestChart(t, `guard:
  enabled: false
  labels: {}
optional:
  enabled: false
`, `{{- if and .Values.guard.enabled (not .Values.guard.labels) }}
{{ fail "guard.labels must be specified when guard.enabled is true" }}
{{- end }}
{{- if .Values.optional.enabled }}
apiVersion: v1
kind: Pod
metadata:
  name: optional
spec:
  containers:
    - name: optional
      image: quay.io/example/optional:v1
{{- end }}
`)

	discovery, err := NewRunner().discoverOptionalChartImages(context.Background(), Options{
		Chart: chartDir,
	}, nil)
	if err != nil {
		t.Fatalf("discoverOptionalChartImages() error = %v", err)
	}
	if got, want := discovery.Images, []string{"quay.io/example/optional:v1"}; !slices.Equal(got, want) {
		t.Fatalf("optional images = %v, want %v (warnings: %v)", got, want, discovery.Warnings)
	}
	if !strings.Contains(strings.Join(discovery.Warnings, "\n"), "guard.labels must be specified") {
		t.Fatalf("warnings = %v, want chart validation warning", discovery.Warnings)
	}
	if _, err := NewRunner().renderChartManifest(context.Background(), Options{
		Chart:     chartDir,
		SetValues: []string{"guard.enabled=true"},
	}); err == nil {
		t.Fatal("normal render unexpectedly ignored the chart validation failure")
	}
}

func TestDiscoverOptionalChartImagesSkipsMalformedSyntheticTemplate(t *testing.T) {
	chartDir := writeInventoryTestChartFiles(t, `broken:
  enabled: false
optional:
  enabled: false
`, map[string]string{
		"broken.yaml": `{{- if .Values.broken.enabled }}
apiVersion: v1
kind: ConfigMap
metadata:
  name: broken
spec:
  value: [
{{- end }}
`,
		"optional.yaml": `{{- if .Values.optional.enabled }}
apiVersion: v1
kind: Pod
metadata:
  name: optional
spec:
  containers:
    - name: optional
      image: quay.io/example/optional:v1
{{- end }}
`,
	})

	discovery, err := NewRunner().discoverOptionalChartImages(context.Background(), Options{
		Chart: chartDir,
	}, nil)
	if err != nil {
		t.Fatalf("discoverOptionalChartImages() error = %v", err)
	}
	if got, want := discovery.Images, []string{"quay.io/example/optional:v1"}; !slices.Equal(got, want) {
		t.Fatalf("optional images = %v, want %v (warnings: %v)", got, want, discovery.Warnings)
	}
	if !strings.Contains(strings.Join(discovery.Warnings, "\n"), "broken.yaml") {
		t.Fatalf("warnings = %v, want malformed template warning", discovery.Warnings)
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
	if got, want := discovery.OptionalFlags["quay.io/example/metrics:v1"], []string{".Values.first.enabled", ".Values.second.enabled"}; !slices.Equal(got, want) {
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

func TestAttributeOptionalImagesStopsAtRenderProbeBudget(t *testing.T) {
	paths := make([]valuePath, maxOptionalImageAttributionProbes+8)
	for index := range paths {
		paths[index] = valuePath{{key: fmt.Sprintf("flag%d", index), isKey: true}}
	}

	runner := NewRunner()
	probeCalls := 0
	runner.renderManifestValues = func(_ Runner, _ context.Context, _ Options, _ map[string]interface{}) (string, error) {
		probeCalls++
		return "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: probe\n", nil
	}
	runner.extractImages = func(string) ([]string, error) {
		return nil, nil
	}
	_, warnings := runner.attributeOptionalImages(
		context.Background(),
		Options{OptionalImageTimeout: time.Second},
		map[string]interface{}{},
		paths,
		map[string]struct{}{},
		map[string]struct{}{"quay.io/example/optional:v1": {}},
	)

	if probeCalls != maxOptionalImageAttributionProbes {
		t.Fatalf("probe calls = %d, want %d", probeCalls, maxOptionalImageAttributionProbes)
	}
	if !strings.Contains(strings.Join(warnings, "\n"), "stopped after 32 render probes") {
		t.Fatalf("warnings = %v, want probe-budget warning", warnings)
	}
}

func TestAttributeOptionalImagesUsesPartialProbeManifest(t *testing.T) {
	paths := []valuePath{
		{{key: "first", isKey: true}},
		{{key: "second", isKey: true}},
	}
	runner := NewRunner()
	runner.renderManifestValues = func(_ Runner, _ context.Context, _ Options, values map[string]interface{}) (string, error) {
		if _, enabled := values["first"]; enabled {
			return "partial first", &partialRenderError{warnings: []string{"synthetic render validation warning: guard failed"}}
		}
		return "partial second", &partialRenderError{warnings: []string{"synthetic render validation warning: unrelated guard failed"}}
	}
	runner.extractImages = func(manifest string) ([]string, error) {
		if manifest == "partial first" {
			return []string{"quay.io/example/optional:v1"}, nil
		}
		return nil, nil
	}

	flags, warnings := runner.attributeOptionalImages(
		context.Background(),
		Options{OptionalImageTimeout: time.Second},
		map[string]interface{}{},
		paths,
		map[string]struct{}{},
		map[string]struct{}{"quay.io/example/optional:v1": {}},
	)

	if got, want := flags["quay.io/example/optional:v1"], []string{".Values.first"}; !slices.Equal(got, want) {
		t.Fatalf("flags = %v, want %v", flags, want)
	}
	if !strings.Contains(strings.Join(warnings, "\n"), "guard failed") {
		t.Fatalf("warnings = %v, want partial-render warning", warnings)
	}
}

func TestImageInventoryPreservesOrderAndRequiredStatus(t *testing.T) {
	got := newImageInventory(
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

	if got, want := got.requiredImages, []string{"quay.io/example/shared:v1"}; !slices.Equal(got, want) {
		t.Fatalf("requiredImages = %v, want %v", got, want)
	}
	if got, want := got.optionalImages, []string{"quay.io/example/annotation:v1", "quay.io/example/optional:v1"}; !slices.Equal(got, want) {
		t.Fatalf("optionalImages = %v, want %v", got, want)
	}

	specs := []pushspec.ArchiveSpec{
		{Image: "quay.io/example/annotation:v1"},
		{Image: "quay.io/example/shared:v1"},
		{Image: "quay.io/example/optional:v1"},
	}
	got.applyMetadata(specs)
	want := []pushspec.ArchiveSpec{
		{Image: "quay.io/example/annotation:v1", Optional: true},
		{Image: "quay.io/example/shared:v1"},
		{Image: "quay.io/example/optional:v1", Optional: true, OptionalFlags: []string{".Values.optional.enabled"}},
	}
	if !reflect.DeepEqual(specs, want) {
		t.Fatalf("applyMetadata() = %#v, want %#v", specs, want)
	}
}

func TestImageInventoryMergesOptionalFlags(t *testing.T) {
	got := newImageInventory(
		[]string{"quay.io/example/chart-only:v1"},
		[]string{"quay.io/example/required:v1"},
		optionalImageDiscovery{
			Images: []string{
				"quay.io/example/chart-only:v1",
				"quay.io/example/optional:v1",
				"quay.io/example/required:v1",
			},
			OptionalFlags: map[string][]string{
				"quay.io/example/chart-only:v1": {".Values.first", ".Values.first", ".Values.second"},
				"quay.io/example/optional:v1":   {".Values.optional"},
				"quay.io/example/required:v1":   {".Values.ignored"},
			},
		},
	)

	specs := []pushspec.ArchiveSpec{
		{Image: "quay.io/example/chart-only:v1"},
		{Image: "quay.io/example/required:v1"},
		{Image: "quay.io/example/optional:v1"},
	}
	got.applyMetadata(specs)
	want := []pushspec.ArchiveSpec{
		{Image: "quay.io/example/chart-only:v1", Optional: true, OptionalFlags: []string{".Values.first", ".Values.second"}},
		{Image: "quay.io/example/required:v1"},
		{Image: "quay.io/example/optional:v1", Optional: true, OptionalFlags: []string{".Values.optional"}},
	}
	if !reflect.DeepEqual(specs, want) {
		t.Fatalf("applyMetadata() = %#v, want %#v", specs, want)
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
	if !slices.Equal(got, want) {
		t.Fatalf("recursive annotation images = %v, want %v", got, want)
	}
}

func writeInventoryTestChart(t *testing.T, values, template string) string {
	t.Helper()
	return writeInventoryTestChartFiles(t, values, map[string]string{"deployment.yaml": template})
}

func writeInventoryTestChartFiles(t *testing.T, values string, templates map[string]string) string {
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
	for name, template := range templates {
		if err := os.WriteFile(filepath.Join(chartDir, "templates", name), []byte(template), 0o644); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}
	return chartDir
}
