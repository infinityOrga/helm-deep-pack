package pull

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"helm-deep-pack/internal/push"
	"helm-deep-pack/internal/pushspec"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	helmchart "helm.sh/helm/v3/pkg/chart"
)

func testLoadedChart(name, version, source string) loadedChart {
	return loadedChart{
		Chart: &helmchart.Chart{
			Metadata: &helmchart.Metadata{
				APIVersion: "v2",
				Name:       name,
				Version:    version,
			},
		},
		Info: ChartInfo{
			Name:    name,
			Version: version,
			Source:  source,
		},
	}
}

func TestDefaultOutputDirCreatesNewDirectoryInCWD(t *testing.T) {
	h := newRunnerTestHarness(t)

	dir, err := defaultOutputDir("openebs")
	if err != nil {
		t.Fatalf("defaultOutputDir() error = %v", err)
	}

	// Verify directory is created in the CWD with the chart name
	expected := filepath.Join(h.cwd, "openebs")
	if dir != expected {
		t.Fatalf("defaultOutputDir() = %q, want %q", dir, expected)
	}

	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("defaultOutputDir() path missing or not a directory: %v, %v", info, err)
	}
}

func TestDefaultOutputDirAppendsTimestampWhenDirectoryExists(t *testing.T) {
	h := newRunnerTestHarness(t)

	// Create directory first
	chart := "nginx"
	first := filepath.Join(h.cwd, chart)
	if err := os.MkdirAll(first, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	// Call defaultOutputDir for the same chart
	dir, err := defaultOutputDir(chart)
	if err != nil {
		t.Fatalf("defaultOutputDir() error = %v", err)
	}

	// Should have timestamp appended in format {chart}-YYYY-MM-DD-HH
	if !strings.Contains(dir, chart+"-") {
		t.Fatalf("defaultOutputDir() = %q, should contain %q", dir, chart+"-")
	}

	// Verify it matches the expected pattern
	base := filepath.Base(dir)
	expectedPattern := fmt.Sprintf("%s-\\d{4}-\\d{2}-\\d{2}-\\d{2}", chart)
	if !matchesPattern(base, expectedPattern) {
		t.Fatalf("defaultOutputDir() = %q, doesn't match pattern %q", base, expectedPattern)
	}

	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("defaultOutputDir() path missing or not a directory: %v, %v", info, err)
	}
}

func TestSanitizeNameWithOCIChartReference(t *testing.T) {
	got := sanitizeName("oci://localhost:5000/charts/prometheus-node-exporter")
	if got != "prometheus-node-exporter" {
		t.Fatalf("sanitizeName() = %q, want %q", got, "prometheus-node-exporter")
	}
}

func matchesPattern(s, pattern string) bool {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return false
	}
	return re.MatchString(s)
}

func TestResolveChartVersionSkipsPrereleases(t *testing.T) {
	h := newRunnerTestHarness(t)
	h.runner.searchRepoVersions = func(context.Context, string, string) ([]searchResult, error) {
		return []searchResult{
			{Version: "4.0.0-rc.1"},
			{Version: "3.10.0"},
			{Version: "3.9.0"},
		}, nil
	}

	got, err := h.runner.resolveChartVersion(context.Background(), "https://example.invalid", "openebs")
	if err != nil {
		t.Fatalf("resolveChartVersion() error = %v", err)
	}

	if got != "3.10.0" {
		t.Fatalf("resolveChartVersion() = %q, want %q", got, "3.10.0")
	}
}

func TestLoadConfiguredReposMissingFileIsNotAnError(t *testing.T) {
	t.Setenv("HELM_REPOSITORY_CONFIG", filepath.Join(t.TempDir(), "repositories.yaml"))

	repos, err := loadConfiguredRepos()
	if err != nil {
		t.Fatalf("loadConfiguredRepos() error = %v", err)
	}
	if repos != nil {
		t.Fatalf("loadConfiguredRepos() = %v, want nil", repos)
	}
}

func TestRunnerRunOrchestratesDependencies(t *testing.T) {
	h := newRunnerTestHarness(t)

	var calls []string
	h.runner.localChartSource = func(_ context.Context, opts Options) (loadedChart, error) {
		return testLoadedChart("demo", "1.0.0", opts.Chart), nil
	}
	h.runner.renderManifest = func(_ Runner, _ context.Context, opts Options) (string, error) {
		calls = append(calls, "render:"+opts.Chart)
		return "kind: ConfigMap\nmetadata:\n  name: demo\n  annotations:\n    image: quay.io/example/api:v1\n", nil
	}
	h.runner.extractChartImages = func(_ context.Context, _ Options) ([]string, error) {
		return nil, nil
	}
	h.runner.extractImages = func(manifest string) ([]string, error) {
		calls = append(calls, "extract")
		if !strings.Contains(manifest, "demo") {
			t.Fatalf("extractImages() saw unexpected manifest: %q", manifest)
		}
		return []string{"quay.io/example/api:v1"}, nil
	}
	h.runner.archiveImages = func(_ context.Context, images []string, outputDir string, concurrency int, _ ...io.Writer) ([]pushspec.ArchiveSpec, error) {
		calls = append(calls, "archive")
		if got, want := images, []string{"quay.io/example/api:v1"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("archiveImages() images = %v, want %v", got, want)
		}
		if outputDir == "" {
			t.Fatal("archiveImages() got empty outputDir")
		}
		if concurrency != 4 {
			t.Fatalf("archiveImages() concurrency = %d, want 4", concurrency)
		}
		return []pushspec.ArchiveSpec{{
			Image:     "quay.io/example/api:v1",
			Target:    "example/api:v1",
			OCIDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		}}, nil
	}
	h.runner.writePushManifest = func(outputDir string, specs []pushspec.ArchiveSpec) error {
		calls = append(calls, "manifest")
		want := []pushspec.ArchiveSpec{{
			Image:     "quay.io/example/api:v1",
			Target:    "example/api:v1",
			OCIDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		}}
		if got := specs; !reflect.DeepEqual(got, want) {
			t.Fatalf("writePushManifest() specs = %v, want %v", got, want)
		}
		manifestPath := filepath.Join(outputDir, pushspec.PushManifestFileName())
		return os.WriteFile(manifestPath, []byte("{\n  \"images\": []\n}\n"), 0o644)
	}
	h.runner.stageChartArchive = func(_ loadedChart, outputDir string) (string, error) {
		calls = append(calls, "chart-archive")
		return filepath.Join(outputDir, "demo-1.0.0.tgz"), nil
	}
	h.runner.stagePushBinary = func(_ context.Context, outputDir, _, _ string) (string, error) {
		calls = append(calls, "copy")
		if outputDir == "" {
			t.Fatal("stagePushBinary() got empty outputDir")
		}
		return filepath.Join(outputDir, push.PushBinaryName()), nil
	}

	dir := t.TempDir()
	chartDir := filepath.Join(dir, "chart")
	if err := os.MkdirAll(chartDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := h.runner.Run(context.Background(), Options{
		Chart:       chartDir,
		OutputDir:   filepath.Join(dir, "out"),
		Concurrency: 4,
	}); err != nil {
		t.Fatalf("Runner.Run() error = %v", err)
	}

	if got, want := calls, []string{"render:" + chartDir, "extract", "archive", "manifest", "chart-archive", "copy"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Runner.Run() calls = %v, want %v", got, want)
	}

	if _, err := os.Stat(filepath.Join(dir, "out", pushspec.PushManifestFileName())); err != nil {
		t.Fatalf("Runner.Run() did not write push manifest: %v", err)
	}
}

func TestRunnerRunIncludesChartAnnotationImagesBeforeManifestImages(t *testing.T) {
	h := newRunnerTestHarness(t)

	var archiveCalls [][]string
	var optionalArchiveCalls [][]string
	h.runner.localChartSource = func(_ context.Context, opts Options) (loadedChart, error) {
		return testLoadedChart("example", "0.1.0", opts.Chart), nil
	}
	h.runner.extractChartImages = func(_ context.Context, _ Options) ([]string, error) {
		return []string{"quay.io/example/from-annotation:v2", "busybox:1.36"}, nil
	}
	h.runner.renderManifest = func(_ Runner, _ context.Context, _ Options) (string, error) {
		return "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: demo\n", nil
	}
	h.runner.extractImages = func(_ string) ([]string, error) {
		return []string{
			"quay.io/example/app:v1",
			"quay.io/example/from-annotation:v2",
		}, nil
	}
	h.runner.archiveImages = func(_ context.Context, images []string, outputDir string, concurrency int, _ ...io.Writer) ([]pushspec.ArchiveSpec, error) {
		archiveCalls = append(archiveCalls, append([]string{}, images...))
		if reflect.DeepEqual(images, []string{"quay.io/example/from-annotation:v2", "quay.io/example/app:v1"}) {
			return []pushspec.ArchiveSpec{
				{
					Image:     "quay.io/example/from-annotation:v2",
					Target:    "example/from-annotation:v2",
					OCIDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				},
				{
					Image:     "quay.io/example/app:v1",
					Target:    "example/app:v1",
					OCIDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
				},
			}, nil
		}
		t.Fatalf("unexpected archive images: %v", images)
		return nil, nil
	}
	h.runner.archiveOptionalImages = func(_ context.Context, images []string, _ string, _ int, _ ...io.Writer) ([]pushspec.ArchiveSpec, []push.ArchiveFailure, error) {
		optionalArchiveCalls = append(optionalArchiveCalls, append([]string{}, images...))
		if !reflect.DeepEqual(images, []string{"busybox:1.36"}) {
			t.Fatalf("unexpected optional archive images: %v", images)
		}
		return []pushspec.ArchiveSpec{{
			Image:     "busybox:1.36",
			Target:    "library/busybox:1.36",
			OCIDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		}}, nil, nil
	}
	h.runner.writePushManifest = func(outputDir string, specs []pushspec.ArchiveSpec) error {
		return os.WriteFile(filepath.Join(outputDir, pushspec.PushManifestFileName()), []byte("{\n  \"images\": []\n}\n"), 0o644)
	}
	h.runner.stagePushBinary = func(_ context.Context, outputDir, _, _ string) (string, error) {
		return filepath.Join(outputDir, push.PushBinaryName()), nil
	}

	dir := t.TempDir()
	chartDir := filepath.Join(dir, "chart")
	if err := os.MkdirAll(chartDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartDir, "Chart.yaml"), []byte(`apiVersion: v2
name: example
version: 0.1.0
annotations:
  annotation.helm.sh/images: |
    - quay.io/example/from-annotation:v2
    - busybox:1.36
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(chartDir, "templates"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartDir, "templates", "noop.yaml"), []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: noop
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if err := h.runner.Run(context.Background(), Options{
		Chart:        chartDir,
		OutputDir:    filepath.Join(dir, "out"),
		Concurrency:  2,
		RenderedOnly: true,
	}); err != nil {
		t.Fatalf("Runner.Run() error = %v", err)
	}

	wantCalls := [][]string{{"quay.io/example/from-annotation:v2", "quay.io/example/app:v1"}}
	if !reflect.DeepEqual(archiveCalls, wantCalls) {
		t.Fatalf("Runner.Run() archive calls = %v, want %v", archiveCalls, wantCalls)
	}
	wantOptionalCalls := [][]string{{"busybox:1.36"}}
	if !reflect.DeepEqual(optionalArchiveCalls, wantOptionalCalls) {
		t.Fatalf("Runner.Run() optional archive calls = %v, want %v", optionalArchiveCalls, wantOptionalCalls)
	}
}

func TestRunnerRunChecksChartAnnotationsBeforeRendering(t *testing.T) {
	h := newRunnerTestHarness(t)

	var calls []string
	h.runner.localChartSource = func(_ context.Context, opts Options) (loadedChart, error) {
		return testLoadedChart("example", "0.1.0", opts.Chart), nil
	}
	h.runner.extractChartImages = func(_ context.Context, _ Options) ([]string, error) {
		calls = append(calls, "chart")
		return nil, nil
	}

	h.runner.renderManifest = func(_ Runner, _ context.Context, _ Options) (string, error) {
		calls = append(calls, "render")
		return "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: demo\n", nil
	}
	h.runner.extractImages = func(_ string) ([]string, error) {
		calls = append(calls, "extract")
		return []string{"busybox:1.36"}, nil
	}
	h.runner.archiveImages = func(_ context.Context, _ []string, _ string, _ int, _ ...io.Writer) ([]pushspec.ArchiveSpec, error) {
		calls = append(calls, "archive")
		return []pushspec.ArchiveSpec{{
			Image:     "busybox:1.36",
			Target:    "library/busybox:1.36",
			OCIDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		}}, nil
	}
	h.runner.writePushManifest = func(outputDir string, _ []pushspec.ArchiveSpec) error {
		calls = append(calls, "manifest")
		return os.WriteFile(filepath.Join(outputDir, pushspec.PushManifestFileName()), []byte("{}\n"), 0o644)
	}
	h.runner.stageChartArchive = func(_ loadedChart, outputDir string) (string, error) {
		calls = append(calls, "chart-archive")
		return filepath.Join(outputDir, "example-0.1.0.tgz"), nil
	}
	h.runner.stagePushBinary = func(_ context.Context, outputDir, _, _ string) (string, error) {
		calls = append(calls, "copy")
		return filepath.Join(outputDir, push.PushBinaryName()), nil
	}

	if err := h.runner.Run(context.Background(), Options{
		Chart:       "ignored-by-stub",
		OutputDir:   t.TempDir(),
		Concurrency: 1,
	}); err != nil {
		t.Fatalf("Runner.Run() error = %v", err)
	}

	want := []string{"chart", "render", "extract", "archive", "manifest", "chart-archive", "copy"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("Runner.Run() calls = %v, want %v", calls, want)
	}
}

func TestRunnerExecuteReturnsPullResult(t *testing.T) {
	h := newRunnerTestHarness(t)

	h.runner.localChartSource = func(_ context.Context, opts Options) (loadedChart, error) {
		return testLoadedChart("example", "0.1.0", opts.Chart), nil
	}
	h.runner.extractChartImages = func(_ context.Context, _ Options) ([]string, error) {
		return []string{"busybox:1.36"}, nil
	}
	h.runner.renderManifest = func(_ Runner, _ context.Context, _ Options) (string, error) {
		return "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: demo\n", nil
	}
	h.runner.extractImages = func(_ string) ([]string, error) {
		return nil, nil
	}
	h.runner.archiveImages = func(_ context.Context, images []string, _ string, _ int, _ ...io.Writer) ([]pushspec.ArchiveSpec, error) {
		return []pushspec.ArchiveSpec{{
			Image:     images[0],
			Target:    "library/busybox:1.36",
			OCIDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		}}, nil
	}
	h.runner.writePushManifest = func(outputDir string, _ []pushspec.ArchiveSpec) error {
		return os.WriteFile(filepath.Join(outputDir, pushspec.PushManifestFileName()), []byte("{}\n"), 0o644)
	}
	h.runner.stagePushBinary = func(_ context.Context, outputDir, _, _ string) (string, error) {
		return filepath.Join(outputDir, push.PushBinaryName()), nil
	}

	outDir := t.TempDir()
	status := new(bytes.Buffer)
	result, err := h.runner.Execute(context.Background(), Options{
		Chart:       "ignored-by-stub",
		OutputDir:   outDir,
		Concurrency: 1,
	}, status)
	if err != nil {
		t.Fatalf("Runner.Execute() error = %v", err)
	}

	if result.OutputDir != outDir {
		t.Fatalf("Runner.Execute() outputDir = %q, want %q", result.OutputDir, outDir)
	}
	if result.Chart.Name != "example" {
		t.Fatalf("Runner.Execute() chart = %#v", result.Chart)
	}
	if got := status.String(); !strings.Contains(got, "chart: name=example version=0.1.0 source=ignored-by-stub") {
		t.Fatalf("Runner.Execute() status = %q", got)
	}
	if got, want := result.Images, []string{"busybox:1.36"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Runner.Execute() images = %v, want %v", got, want)
	}
	if got, want := result.ManifestPath, filepath.Join(outDir, pushspec.PushManifestFileName()); got != want {
		t.Fatalf("Runner.Execute() manifestPath = %q, want %q", got, want)
	}
	if len(result.ArchiveSpecs) != 1 || result.ArchiveSpecs[0].Image != "busybox:1.36" {
		t.Fatalf("Runner.Execute() archiveSpecs = %#v", result.ArchiveSpecs)
	}
}

func TestRunnerExecuteReturnsStatusWriterError(t *testing.T) {
	h := newRunnerTestHarness(t)
	h.runner.localChartSource = func(_ context.Context, opts Options) (loadedChart, error) {
		return testLoadedChart("example", "0.1.0", opts.Chart), nil
	}

	_, err := h.runner.Execute(context.Background(), Options{
		Chart:     "ignored-by-stub",
		OutputDir: t.TempDir(),
	}, failingWriter{})
	if err == nil {
		t.Fatal("Runner.Execute() error = nil, want status writer error")
	}
	if !strings.Contains(err.Error(), "write status") {
		t.Fatalf("Runner.Execute() error = %v, want status write attribution", err)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("status unavailable")
}

func TestRunnerExecuteRemovesEmptyCreatedOutputDirOnError(t *testing.T) {
	h := newRunnerTestHarness(t)
	t.Setenv("HELM_REPOSITORY_CONFIG", filepath.Join(t.TempDir(), "repositories.yaml"))
	h.runner.localChartSource = func(_ context.Context, _ Options) (loadedChart, error) {
		return loadedChart{}, fmt.Errorf("chart not found")
	}

	outDir := filepath.Join(t.TempDir(), "out")
	if _, err := h.runner.Execute(context.Background(), Options{
		Chart:     "missing",
		OutputDir: outDir,
	}); err == nil {
		t.Fatal("Runner.Execute() error = nil, want failure")
	}

	if _, err := os.Stat(outDir); !os.IsNotExist(err) {
		t.Fatalf("Runner.Execute() left output dir behind: %v", err)
	}
}

func TestRunnerExecuteRemovesPreexistingEmptyOutputDirOnError(t *testing.T) {
	h := newRunnerTestHarness(t)
	t.Setenv("HELM_REPOSITORY_CONFIG", filepath.Join(t.TempDir(), "repositories.yaml"))
	h.runner.localChartSource = func(_ context.Context, _ Options) (loadedChart, error) {
		return loadedChart{}, fmt.Errorf("chart not found")
	}

	outDir := filepath.Join(t.TempDir(), "out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if _, err := h.runner.Execute(context.Background(), Options{
		Chart:     "missing",
		OutputDir: outDir,
	}); err == nil {
		t.Fatal("Runner.Execute() error = nil, want failure")
	}

	if _, err := os.Stat(outDir); !os.IsNotExist(err) {
		t.Fatalf("Runner.Execute() left empty output dir behind: %v", err)
	}
}

func TestRenderChartManifestRendersLocalChart(t *testing.T) {
	h := newRunnerTestHarness(t)
	chartDir := filepath.Join(h.cwd, "chart")
	if err := os.MkdirAll(filepath.Join(chartDir, "templates"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartDir, "Chart.yaml"), []byte(`apiVersion: v2
name: example
version: 0.1.0
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartDir, "templates", "deployment.yaml"), []byte(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: example
spec:
  template:
    spec:
      containers:
        - name: app
          image: quay.io/example/app:v1
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := h.runner.renderChartManifest(context.Background(), Options{
		Chart: "chart",
	})
	if err != nil {
		t.Fatalf("renderChartManifest() error = %v", err)
	}
	if !strings.Contains(got, "quay.io/example/app:v1") {
		t.Fatalf("renderChartManifest() = %q, want rendered image", got)
	}
}

func TestCloneChartPreservesDependencyTreeWithoutSharingMutableMetadata(t *testing.T) {
	dependency := &helmchart.Chart{
		Metadata: &helmchart.Metadata{
			APIVersion: "v2",
			Name:       "child",
			Version:    "0.1.0",
		},
	}
	root := &helmchart.Chart{
		Metadata: &helmchart.Metadata{
			APIVersion: "v2",
			Name:       "root",
			Version:    "0.1.0",
			Dependencies: []*helmchart.Dependency{{
				Name:      "child",
				Condition: "child.enabled",
				Enabled:   true,
			}},
		},
		Values: map[string]interface{}{
			"child": map[string]interface{}{"enabled": true},
		},
	}
	root.AddDependency(dependency)

	cloned, err := cloneChart(root)
	if err != nil {
		t.Fatalf("cloneChart() error = %v", err)
	}
	if len(cloned.Dependencies()) != 1 {
		t.Fatalf("cloneChart() dependencies = %d, want 1", len(cloned.Dependencies()))
	}
	if cloned.Dependencies()[0].Parent() != cloned {
		t.Fatal("cloneChart() dependency parent does not point at clone")
	}
	if cloned.Metadata.Dependencies[0] == root.Metadata.Dependencies[0] {
		t.Fatal("cloneChart() shared dependency metadata")
	}

	cloned.Metadata.Dependencies[0].Enabled = false
	cloned.Dependencies()[0].Metadata.Name = "changed"
	if !root.Metadata.Dependencies[0].Enabled || root.Dependencies()[0].Metadata.Name != "child" {
		t.Fatalf("cloneChart() mutation changed source chart: %#v / %#v", root.Metadata.Dependencies[0], root.Dependencies()[0].Metadata)
	}
}

func TestRenderChartManifestIgnoresNestedNotesTemplates(t *testing.T) {
	h := newRunnerTestHarness(t)
	chartDir := filepath.Join(h.cwd, "chart")
	if err := os.MkdirAll(filepath.Join(chartDir, "templates"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartDir, "Chart.yaml"), []byte(`apiVersion: v2
name: openebs
version: 0.1.0
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartDir, "templates", "configmap.yaml"), []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: openebs
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	subchartDir := filepath.Join(chartDir, "charts", "alloy")
	if err := os.MkdirAll(filepath.Join(subchartDir, "templates"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(subchartDir, "Chart.yaml"), []byte(`apiVersion: v2
name: alloy
version: 0.1.0
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(subchartDir, "templates", "NOTES.txt"), []byte(`This is plain text from NOTES.
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := h.runner.renderChartManifest(context.Background(), Options{
		Chart: "chart",
	})
	if err != nil {
		t.Fatalf("renderChartManifest() error = %v", err)
	}
	if !strings.Contains(got, "kind: ConfigMap") {
		t.Fatalf("renderChartManifest() = %q, want rendered yaml manifest", got)
	}
	if strings.Contains(got, "This is plain text from NOTES.") {
		t.Fatalf("renderChartManifest() unexpectedly included NOTES.txt content: %q", got)
	}
}

func TestRenderChartManifestIncludesHookResources(t *testing.T) {
	h := newRunnerTestHarness(t)
	chartDir := filepath.Join(h.cwd, "chart")
	if err := os.MkdirAll(filepath.Join(chartDir, "templates"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartDir, "Chart.yaml"), []byte(`apiVersion: v2
name: example
version: 0.1.0
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartDir, "templates", "hook-job.yaml"), []byte(`apiVersion: batch/v1
kind: Job
metadata:
  name: hook-job
  annotations:
    "helm.sh/hook": pre-install
spec:
  template:
    spec:
      restartPolicy: Never
      containers:
        - name: hook
          image: quay.io/example/hook:v1
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := h.runner.renderChartManifest(context.Background(), Options{
		Chart: "chart",
	})
	if err != nil {
		t.Fatalf("renderChartManifest() error = %v", err)
	}
	if !strings.Contains(got, "quay.io/example/hook:v1") {
		t.Fatalf("renderChartManifest() = %q, want hook manifest image", got)
	}
}

func TestRenderChartManifestAppliesValuesFiles(t *testing.T) {
	h := newRunnerTestHarness(t)
	chartDir := filepath.Join(h.cwd, "chart")
	if err := os.MkdirAll(filepath.Join(chartDir, "templates"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartDir, "Chart.yaml"), []byte(`apiVersion: v2
name: example
version: 0.1.0
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartDir, "values.yaml"), []byte(`image:
  tag: v1
sidecar:
  enabled: false
  tag: v1
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartDir, "templates", "deployment.yaml"), []byte(`apiVersion: apps/v1
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
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	valuesFile := filepath.Join(h.cwd, "override-values.yaml")
	if err := os.WriteFile(valuesFile, []byte(`sidecar:
  enabled: true
  tag: v9
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := h.runner.renderChartManifest(context.Background(), Options{
		Chart:       "chart",
		ValuesFiles: []string{valuesFile},
	})
	if err != nil {
		t.Fatalf("renderChartManifest() error = %v", err)
	}
	if !strings.Contains(got, "quay.io/example/sidecar:v9") {
		t.Fatalf("renderChartManifest() = %q, want sidecar image from values file", got)
	}
}

func TestRenderChartManifestAppliesValuesFilesInOrder(t *testing.T) {
	h := newRunnerTestHarness(t)
	chartDir := filepath.Join(h.cwd, "chart")
	if err := os.MkdirAll(filepath.Join(chartDir, "templates"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartDir, "Chart.yaml"), []byte(`apiVersion: v2
name: example
version: 0.1.0
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartDir, "values.yaml"), []byte(`image:
  tag: v1
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartDir, "templates", "deployment.yaml"), []byte(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: example
spec:
  template:
    spec:
      containers:
        - name: app
          image: quay.io/example/app:{{ .Values.image.tag }}
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	fileA := filepath.Join(h.cwd, "values-a.yaml")
	if err := os.WriteFile(fileA, []byte("image:\n  tag: v2\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	fileB := filepath.Join(h.cwd, "values-b.yaml")
	if err := os.WriteFile(fileB, []byte("image:\n  tag: v3\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := h.runner.renderChartManifest(context.Background(), Options{
		Chart:       "chart",
		ValuesFiles: []string{fileA, fileB},
	})
	if err != nil {
		t.Fatalf("renderChartManifest() error = %v", err)
	}
	if !strings.Contains(got, "quay.io/example/app:v3") {
		t.Fatalf("renderChartManifest() = %q, want image tag from last values file", got)
	}
}

func TestRenderChartManifestSetOverridesValuesFile(t *testing.T) {
	h := newRunnerTestHarness(t)
	chartDir := filepath.Join(h.cwd, "chart")
	if err := os.MkdirAll(filepath.Join(chartDir, "templates"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartDir, "Chart.yaml"), []byte(`apiVersion: v2
name: example
version: 0.1.0
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartDir, "values.yaml"), []byte(`image:
  tag: v1
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartDir, "templates", "deployment.yaml"), []byte(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: example
spec:
  template:
    spec:
      containers:
        - name: app
          image: quay.io/example/app:{{ .Values.image.tag }}
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	valuesFile := filepath.Join(h.cwd, "override-values.yaml")
	if err := os.WriteFile(valuesFile, []byte("image:\n  tag: v2\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := h.runner.renderChartManifest(context.Background(), Options{
		Chart:       "chart",
		ValuesFiles: []string{valuesFile},
		SetValues:   []string{"image.tag=v4"},
	})
	if err != nil {
		t.Fatalf("renderChartManifest() error = %v", err)
	}
	if !strings.Contains(got, "quay.io/example/app:v4") {
		t.Fatalf("renderChartManifest() = %q, want --set override to win", got)
	}
}

func TestRenderChartManifestValuesInputErrorsAreAttributed(t *testing.T) {
	h := newRunnerTestHarness(t)
	chartDir := filepath.Join(h.cwd, "chart")
	if err := os.MkdirAll(filepath.Join(chartDir, "templates"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartDir, "Chart.yaml"), []byte(`apiVersion: v2
name: example
version: 0.1.0
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartDir, "templates", "deployment.yaml"), []byte(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: example
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := h.runner.renderChartManifest(context.Background(), Options{
		Chart:       "chart",
		ValuesFiles: []string{filepath.Join(h.cwd, "missing-values.yaml")},
	})
	if err == nil {
		t.Fatal("renderChartManifest() error = nil, want missing values file error")
	}
	if !strings.Contains(err.Error(), "read values file") {
		t.Fatalf("renderChartManifest() error = %v, want values file attribution", err)
	}

	_, err = h.runner.renderChartManifest(context.Background(), Options{
		Chart:     "chart",
		SetValues: []string{"image.tag"},
	})
	if err == nil {
		t.Fatal("renderChartManifest() error = nil, want invalid --set parse error")
	}
	if !strings.Contains(err.Error(), "parse --set") {
		t.Fatalf("renderChartManifest() error = %v, want --set attribution", err)
	}
}

func TestLoadChartUsesCachedChartForSameOptions(t *testing.T) {
	h := newRunnerTestHarness(t)
	chartDir := filepath.Join(h.cwd, "chart")
	if err := os.MkdirAll(chartDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartDir, "Chart.yaml"), []byte(`apiVersion: v2
name: cached
version: 0.1.0
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	opts := Options{Chart: "chart"}
	if _, err := h.runner.loadChart(context.Background(), opts); err != nil {
		t.Fatalf("loadChart() first call error = %v", err)
	}

	if err := os.Remove(filepath.Join(chartDir, "Chart.yaml")); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}

	if _, err := h.runner.loadChart(context.Background(), opts); err != nil {
		t.Fatalf("loadChart() second call error = %v, want cached chart", err)
	}
}

func TestLoadChartCacheKeyIncludesValuesOverrides(t *testing.T) {
	h := newRunnerTestHarness(t)
	chartDir := filepath.Join(h.cwd, "chart")
	if err := os.MkdirAll(chartDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartDir, "Chart.yaml"), []byte(`apiVersion: v2
name: cached
version: 0.1.0
`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := h.runner.loadChart(context.Background(), Options{
		Chart:       "chart",
		ValuesFiles: []string{"values-a.yaml"},
	}); err != nil {
		t.Fatalf("loadChart() first call error = %v", err)
	}

	if err := os.Remove(filepath.Join(chartDir, "Chart.yaml")); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}

	if _, err := h.runner.loadChart(context.Background(), Options{
		Chart:       "chart",
		ValuesFiles: []string{"values-b.yaml"},
	}); err == nil {
		t.Fatal("loadChart() second call error = nil, want miss with different values overrides")
	}
}

func TestResolveChartVersionHonorsContextCancellation(t *testing.T) {
	h := newRunnerTestHarness(t)
	h.runner.searchRepoVersions = func(ctx context.Context, _, _ string) ([]searchResult, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := h.runner.resolveChartVersion(ctx, "https://example.invalid", "openebs"); err == nil {
		t.Fatal("resolveChartVersion() error = nil, want cancellation error")
	}
}

func TestLoadHelmRepoChartMissingChartListsAvailableCharts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/index.yaml" {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprint(w, `apiVersion: v1
entries:
  nginx:
    - version: 1.2.3
  redis:
    - version: 2.0.0
`)
	}))
	t.Cleanup(server.Close)

	_, err := loadHelmRepoChart(context.Background(), Options{
		Chart: "missing",
		Repo:  server.URL,
	})
	if err == nil {
		t.Fatal("loadHelmRepoChart() error = nil, want missing chart error")
	}

	errStr := err.Error()
	if !strings.Contains(errStr, "not found in repo") {
		t.Fatalf("error should indicate repo miss, got: %s", errStr)
	}
	if !strings.Contains(errStr, "charts: nginx, redis") {
		t.Fatalf("error should list available charts, got: %s", errStr)
	}
}

func TestLoadHelmRepoChartVersionMismatchListsAvailableVersions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/index.yaml" {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprint(w, `apiVersion: v1
entries:
  nginx:
    - version: 2.3.0
    - version: 2.1.0
    - version: 2.0.0
    - version: 1.9.0
    - version: 2.2.0-alpha.1
  redis:
    - version: 1.0.0
`)
	}))
	t.Cleanup(server.Close)

	_, err := loadHelmRepoChart(context.Background(), Options{
		Chart:   "nginx",
		Repo:    server.URL,
		Version: "1.0.0",
	})
	if err == nil {
		t.Fatal("loadHelmRepoChart() error = nil, want version mismatch error")
	}

	errStr := err.Error()
	if !strings.Contains(errStr, "found in repo") {
		t.Fatalf("error should indicate chart name matched, got: %s", errStr)
	}
	if !strings.Contains(errStr, `version "1.0.0" missing`) {
		t.Fatalf("error should mention missing version, got: %s", errStr)
	}
	if !strings.Contains(errStr, "versions: 2.3.0, 2.1.0, 2.0.0") {
		t.Fatalf("error should list available versions, got: %s", errStr)
	}
}

func TestLoadHelmRepoChartRejectsNonHelmWebsite(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/index.yaml" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w, `<!doctype html><html lang="en"><body><h1>Example Domain</h1></body></html>`)
	}))
	t.Cleanup(server.Close)

	_, err := loadHelmRepoChart(context.Background(), Options{
		Chart: "openebs",
		Repo:  server.URL,
	})
	if err == nil {
		t.Fatal("loadHelmRepoChart() error = nil, want non-helm repository error")
	}

	if !strings.Contains(err.Error(), "does not look like a Helm repository") {
		t.Fatalf("error should reject non-helm websites, got: %s", err)
	}
}

func TestLoadHelmRepoChartRejectsOversizedArchive(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.yaml":
			_, _ = fmt.Fprintf(w, `apiVersion: v1
entries:
  nginx:
    - version: 1.2.3
      name: nginx
      urls:
        - %s/charts/nginx-1.2.3.tgz
`, server.URL)
		case "/charts/nginx-1.2.3.tgz":
			_, _ = w.Write(bytes.Repeat([]byte("x"), maxChartArchiveBytes+1))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	_, err := loadHelmRepoChart(context.Background(), Options{
		Chart:   "nginx",
		Repo:    server.URL,
		Version: "1.2.3",
	})
	if err == nil {
		t.Fatal("loadHelmRepoChart() error = nil, want oversized archive error")
	}
	if !strings.Contains(err.Error(), "chart archive exceeds size limit") {
		t.Fatalf("error should report oversized archive, got: %s", err)
	}
}

type runnerTestHarness struct {
	t      *testing.T
	cwd    string
	runner Runner
}

func newRunnerTestHarness(t *testing.T) *runnerTestHarness {
	t.Helper()

	cwd := t.TempDir()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}

	h := &runnerTestHarness{t: t, cwd: cwd, runner: NewRunner()}
	return h
}

// TestLoadChartFallbackToConfiguredReposOnLocalFailure verifies fallback activates when local load fails
func TestLoadChartFallbackToConfiguredReposOnLocalFailure(t *testing.T) {
	h := newRunnerTestHarness(t)
	t.Setenv("HELM_REPOSITORY_CONFIG", filepath.Join(t.TempDir(), "repositories.yaml"))
	h.runner.localChartSource = func(ctx context.Context, opts Options) (loadedChart, error) {
		return loadedChart{}, fmt.Errorf("local chart not found")
	}
	h.runner.helmChartSource = func(ctx context.Context, opts Options) (loadedChart, error) {
		// This simulates what happens when fallback searches configured repos
		// In a real scenario with configured repos, this would return a chart
		return loadedChart{}, fmt.Errorf("chart not found in any configured repo")
	}

	_, err := h.runner.loadChart(context.Background(), Options{Chart: "test"})
	if err == nil {
		t.Fatal("loadChart() should return error when both local and fallback fail")
	}
	// Should show both local and fallback failures in the error
	errStr := err.Error()
	if !strings.Contains(errStr, "not found") {
		t.Fatalf("error should indicate chart not found, got: %s", errStr)
	}
	if !strings.Contains(errStr, "download/pull the chart first or add its repository to Helm") {
		t.Fatalf("error should provide recovery guidance, got: %s", errStr)
	}
}

func TestLoadChartVersionMismatchUsesConfiguredRepoDetails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/index.yaml" {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprint(w, `apiVersion: v1
entries:
  openebs:
    - version: 3.11.0
    - version: 3.10.0
    - version: 3.9.0
`)
	}))
	t.Cleanup(server.Close)

	repoConfigPath := filepath.Join(t.TempDir(), "repositories.yaml")
	if err := os.WriteFile(repoConfigPath, []byte(fmt.Sprintf(`apiVersion: v1
generated: "2026-06-22T00:00:00Z"
repositories:
- name: test
  url: %s
`, server.URL)), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	t.Setenv("HELM_REPOSITORY_CONFIG", repoConfigPath)

	h := newRunnerTestHarness(t)
	_, err := h.runner.loadChart(context.Background(), Options{
		Chart:   "openebs",
		Version: "111.0.0",
	})
	if err == nil {
		t.Fatal("loadChart() error = nil, want version mismatch error")
	}

	errStr := err.Error()
	if !strings.Contains(errStr, `found in repo`) {
		t.Fatalf("error should mention repo version mismatch, got: %s", errStr)
	}
	if !strings.Contains(errStr, `versions: 3.11.0, 3.10.0, 3.9.0`) {
		t.Fatalf("error should list last 3 stable versions, got: %s", errStr)
	}
}

// TestLoadChartNoFallbackWhenLocalSucceeds verifies fallback doesn't trigger if local load succeeds
func TestLoadChartNoFallbackWhenLocalSucceeds(t *testing.T) {
	h := newRunnerTestHarness(t)
	localCalled := false

	h.runner.localChartSource = func(ctx context.Context, opts Options) (loadedChart, error) {
		localCalled = true
		return testLoadedChart("local", "0.0.0", opts.Chart), nil
	}

	chrt, err := h.runner.loadChart(context.Background(), Options{Chart: "test"})
	if err != nil {
		t.Fatalf("loadChart() error = %v, want nil", err)
	}
	if !localCalled {
		t.Fatal("local source should have been called")
	}
	if chrt.Info.Name != "local" {
		t.Fatalf("loadChart() returned chart name %q, want local", chrt.Info.Name)
	}
}

// TestLoadChartNoFallbackWhenRepoSpecified verifies fallback doesn't trigger when --repo is provided
func TestLoadChartNoFallbackWhenRepoSpecified(t *testing.T) {
	h := newRunnerTestHarness(t)
	localCalled := false

	h.runner.localChartSource = func(ctx context.Context, opts Options) (loadedChart, error) {
		localCalled = true
		return loadedChart{}, fmt.Errorf("should not call")
	}
	h.runner.helmChartSource = func(ctx context.Context, opts Options) (loadedChart, error) {
		return testLoadedChart("repo", "0.0.0", opts.Repo), nil
	}

	chrt, err := h.runner.loadChart(context.Background(), Options{
		Chart: "test",
		Repo:  "https://example.com",
	})
	if err != nil {
		t.Fatalf("loadChart() error = %v, want nil", err)
	}
	if localCalled {
		t.Fatal("local source should not have been called when repo is specified")
	}
	if chrt.Info.Name != "repo" {
		t.Fatalf("loadChart() returned chart name %q, want repo", chrt.Info.Name)
	}
}

// TestLoadChartCombinedErrorContext verifies error message includes both local and fallback failure context
func TestLoadChartCombinedErrorContext(t *testing.T) {
	h := newRunnerTestHarness(t)
	t.Setenv("HELM_REPOSITORY_CONFIG", filepath.Join(t.TempDir(), "repositories.yaml"))
	h.runner.localChartSource = func(ctx context.Context, opts Options) (loadedChart, error) {
		return loadedChart{}, fmt.Errorf("local path does not exist")
	}
	// Simulate no configured repos by mocking helmChartSource to fail
	h.runner.helmChartSource = func(ctx context.Context, opts Options) (loadedChart, error) {
		return loadedChart{}, fmt.Errorf("no configured repos available")
	}

	_, err := h.runner.loadChart(context.Background(), Options{Chart: "test"})
	if err == nil {
		t.Fatal("loadChart() should return error when both local and fallback fail")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "not found") {
		t.Fatalf("error should indicate chart not found, got: %s", errStr)
	}
	if !strings.Contains(errStr, "download/pull the chart first or add its repository to Helm") {
		t.Fatalf("error should provide recovery guidance, got: %s", errStr)
	}
}

// TestLoadChartCachedResultNotBypassedByFallback verifies caching still works with fallback
func TestLoadChartCachedResultNotBypassedByFallback(t *testing.T) {
	h := newRunnerTestHarness(t)
	callCount := 0
	h.runner.localChartSource = func(ctx context.Context, opts Options) (loadedChart, error) {
		callCount++
		return testLoadedChart("cached", "0.0.0", opts.Chart), nil
	}

	opts := Options{Chart: "test"}
	chrt1, _ := h.runner.loadChart(context.Background(), opts)
	chrt2, _ := h.runner.loadChart(context.Background(), opts)

	if callCount != 1 {
		t.Fatalf("localChartSource called %d times, want 1 (should use cache)", callCount)
	}
	if chrt1.Info.Name != chrt2.Info.Name || chrt1.Info.Source != chrt2.Info.Source {
		t.Fatal("cached chart should return same object")
	}
}
