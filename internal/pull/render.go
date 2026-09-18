package pull

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"path"
	"sort"
	"strings"
	"sync"

	helmchart "helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/engine"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/releaseutil"
	"helm.sh/helm/v3/pkg/strvals"
)

func (r Runner) renderChartManifest(ctx context.Context, opts Options) (string, error) {
	loaded, err := r.loadChart(ctx, opts)
	if err != nil {
		return "", err
	}

	userValues, err := renderUserValues(opts)
	if err != nil {
		return "", err
	}
	return r.renderLoadedChartManifest(loaded.Chart, userValues, false)
}

func (r Runner) renderChartManifestWithValuesForDiscovery(ctx context.Context, opts Options, userValues map[string]interface{}) (string, error) {
	loaded, err := r.loadChart(ctx, opts)
	if err != nil {
		return "", err
	}
	return r.renderLoadedChartManifest(loaded.Chart, userValues, true)
}

func (r Runner) renderLoadedChartManifest(source *helmchart.Chart, userValues map[string]interface{}, lintMode bool) (string, error) {
	chrt, err := cloneChart(source)
	if err != nil {
		return "", err
	}

	if err := chartutil.ProcessDependenciesWithMerge(chrt, chartutil.Values(userValues)); err != nil {
		return "", err
	}

	caps := chartutil.DefaultCapabilities.Copy()
	// Hardcoded release name and namespace: these don't affect image extraction
	// (which is the CLI's sole purpose) as they only influence template rendering
	// metadata. Users cannot customize these values.
	renderValues, err := chartutil.ToRenderValuesWithSchemaValidation(
		chrt,
		userValues,
		chartutil.ReleaseOptions{
			Name:      "mirror",
			Namespace: "default",
			Revision:  1,
			IsInstall: true,
		},
		caps,
		false,
	)
	if err != nil {
		return "", err
	}

	// Keep the normal render strict. Synthetic discovery is inventory-only, so
	// use Helm's lint mode; this lets guards such as kube-prometheus-stack's
	// required Grafana selector produce enough output for image extraction
	// without changing the user's render.
	var renderedFiles map[string]string
	var lintWarnings []string
	if lintMode {
		if r.lintRender == nil {
			return "", fmt.Errorf("render lint mode: no lint renderer configured")
		}
		renderedFiles, lintWarnings, err = r.lintRender(chrt, renderValues)
	} else {
		renderedFiles, err = engine.Render(chrt, renderValues)
	}
	if err != nil {
		return "", err
	}

	removeNotesTemplates(renderedFiles)

	hooks, manifests, err := releaseutil.SortManifests(renderedFiles, nil, releaseutil.InstallOrder)
	if err != nil {
		if lintMode {
			manifest, partialErr := renderBestEffortManifest(chrt, renderedFiles)
			return manifest, appendPartialRenderWarnings(partialErr, lintWarnings)
		}
		return renderDebugManifest(renderedFiles), fmt.Errorf("sort manifests: %w", err)
	}
	manifest := formatRenderedManifest(chrt, hooks, manifests)
	if len(lintWarnings) > 0 {
		return manifest, &partialRenderError{warnings: lintWarnings}
	}
	return manifest, nil
}

type helmLintRenderer struct {
	mu sync.Mutex
}

func (r *helmLintRenderer) Render(chrt *helmchart.Chart, renderValues chartutil.Values) (map[string]string, []string, error) {
	// Helm's engine uses the process-wide standard logger for lint diagnostics
	// and does not expose a writer or logger dependency. Serialize the brief
	// redirection for this runner and keep this SDK limitation behind the
	// injectable lintRender collaborator.
	r.mu.Lock()
	defer r.mu.Unlock()

	previousWriter := log.Writer()
	var lintLog bytes.Buffer
	log.SetOutput(&lintLog)
	defer log.SetOutput(previousWriter)
	rendered, err := (engine.Engine{LintMode: true}).Render(chrt, renderValues)
	return rendered, parseLintWarnings(lintLog.String()), err
}

func parseLintWarnings(output string) []string {
	const infoMarker = "[INFO] "
	seen := make(map[string]struct{})
	warnings := make([]string, 0)
	for _, line := range strings.Split(output, "\n") {
		markerIndex := strings.Index(line, infoMarker)
		if markerIndex < 0 {
			continue
		}
		message := strings.TrimSpace(line[markerIndex+len(infoMarker):])
		if message == "" {
			continue
		}
		if _, ok := seen[message]; ok {
			continue
		}
		seen[message] = struct{}{}
		warnings = append(warnings, fmt.Sprintf("synthetic render validation warning: %s", message))
	}
	return warnings
}

func appendPartialRenderWarnings(partialErr error, warnings []string) error {
	if len(warnings) == 0 {
		return partialErr
	}
	if partialErr == nil {
		return &partialRenderError{warnings: warnings}
	}
	partial, ok := partialErr.(*partialRenderError)
	if !ok {
		return partialErr
	}
	combined := append([]string(nil), warnings...)
	combined = append(combined, partial.warnings...)
	return &partialRenderError{warnings: combined}
}

func formatRenderedManifest(chrt *helmchart.Chart, hooks []*release.Hook, manifests []releaseutil.Manifest) string {
	var out bytes.Buffer
	for _, crd := range chrt.CRDObjects() {
		fmt.Fprintf(&out, "---\n# Source: %s\n%s\n", crd.Filename, string(crd.File.Data))
	}
	for _, hook := range hooks {
		fmt.Fprintf(&out, "---\n# Source: %s\n%s\n", hook.Path, hook.Manifest)
	}
	for _, manifest := range manifests {
		fmt.Fprintf(&out, "---\n# Source: %s\n%s\n", manifest.Name, manifest.Content)
	}
	return out.String()
}

type partialRenderError struct {
	warnings []string
}

func (e *partialRenderError) Error() string {
	return strings.Join(e.warnings, "; ")
}

func renderBestEffortManifest(chrt *helmchart.Chart, renderedFiles map[string]string) (string, error) {
	// A chart can render most templates successfully while one synthetic branch
	// emits invalid YAML. Sort each rendered file independently so that branch
	// does not hide images from every other template.
	files := make([]string, 0, len(renderedFiles))
	for name := range renderedFiles {
		files = append(files, name)
	}
	sort.Strings(files)

	var hooks []*release.Hook
	var manifests []releaseutil.Manifest
	warnings := make([]string, 0)
	for _, name := range files {
		fileHooks, fileManifests, err := releaseutil.SortManifests(
			map[string]string{name: renderedFiles[name]},
			nil,
			releaseutil.InstallOrder,
		)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("optional image discovery skipped unparseable rendered template %q: %v", name, err))
			continue
		}
		hooks = append(hooks, fileHooks...)
		manifests = append(manifests, fileManifests...)
	}
	if len(warnings) == 0 {
		return renderDebugManifest(renderedFiles), nil
	}

	return formatRenderedManifest(chrt, hooks, manifests), &partialRenderError{warnings: warnings}
}

func cloneChart(source *helmchart.Chart) (*helmchart.Chart, error) {
	if source == nil {
		return nil, fmt.Errorf("clone chart: chart is nil")
	}

	// Chart's render inputs (templates, raw files, values, and schema) are
	// immutable during Helm rendering. The dependency state is different:
	// ProcessDependenciesWithMerge mutates dependency metadata and the
	// dependency tree, so clone that small mutable portion explicitly instead
	// of deep-copying every chart file and byte slice.
	cloned := *source
	cloned.Metadata = cloneChartMetadata(source.Metadata)

	// Chart keeps its dependency tree and parent pointer in unexported fields,
	// so a shallow copy does not preserve them. Rebuild that tree through
	// Helm's public API; rendering a clone without dependencies silently loses
	// subchart templates and can also make root templates fail on .Subcharts.
	dependencies := source.Dependencies()
	if len(dependencies) == 0 {
		return &cloned, nil
	}
	clonedDependencies := make([]*helmchart.Chart, len(dependencies))
	for index, dependency := range dependencies {
		clonedDependency, err := cloneChart(dependency)
		if err != nil {
			return nil, err
		}
		clonedDependencies[index] = clonedDependency
	}
	cloned.SetDependencies(clonedDependencies...)
	return &cloned, nil
}

func cloneChartMetadata(source *helmchart.Metadata) *helmchart.Metadata {
	if source == nil {
		return nil
	}
	cloned := *source
	cloned.Sources = append([]string(nil), source.Sources...)
	cloned.Keywords = append([]string(nil), source.Keywords...)
	if len(source.Maintainers) > 0 {
		cloned.Maintainers = make([]*helmchart.Maintainer, len(source.Maintainers))
		for index, maintainer := range source.Maintainers {
			if maintainer == nil {
				continue
			}
			maintainerCopy := *maintainer
			cloned.Maintainers[index] = &maintainerCopy
		}
	}
	if source.Annotations != nil {
		cloned.Annotations = make(map[string]string, len(source.Annotations))
		for key, value := range source.Annotations {
			cloned.Annotations[key] = value
		}
	}
	if len(source.Dependencies) > 0 {
		cloned.Dependencies = make([]*helmchart.Dependency, len(source.Dependencies))
		for index, dependency := range source.Dependencies {
			if dependency == nil {
				continue
			}
			dependencyCopy := *dependency
			dependencyCopy.Tags = append([]string(nil), dependency.Tags...)
			dependencyCopy.ImportValues = append([]interface{}(nil), dependency.ImportValues...)
			cloned.Dependencies[index] = &dependencyCopy
		}
	}
	return &cloned
}

func renderUserValues(opts Options) (map[string]interface{}, error) {
	merged := map[string]interface{}{}

	for _, valuesFile := range opts.ValuesFiles {
		fileValues, err := chartutil.ReadValuesFile(valuesFile)
		if err != nil {
			return nil, fmt.Errorf("read values file %q: %w", valuesFile, err)
		}
		merged = chartutil.MergeTables(fileValues, merged)
	}

	for _, setExpr := range opts.SetValues {
		if err := strvals.ParseInto(setExpr, merged); err != nil {
			return nil, fmt.Errorf("parse --set %q: %w", setExpr, err)
		}
	}

	return merged, nil
}

func removeNotesTemplates(renderedFiles map[string]string) {
	for name := range renderedFiles {
		if path.Base(name) == "NOTES.txt" {
			delete(renderedFiles, name)
		}
	}
}

func renderDebugManifest(files map[string]string) string {
	var b bytes.Buffer
	for name, content := range files {
		if strings.TrimSpace(content) == "" {
			continue
		}
		fmt.Fprintf(&b, "---\n# Source: %s\n%s\n", name, content)
	}
	return b.String()
}
