package pull

import (
	"bytes"
	"context"
	"fmt"
	"path"
	"strings"

	helmchart "helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/engine"
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
	return renderChartManifestWithValues(loaded.Chart, userValues)
}

func (r Runner) renderChartManifestWithValues(ctx context.Context, opts Options, userValues map[string]interface{}) (string, error) {
	loaded, err := r.loadChart(ctx, opts)
	if err != nil {
		return "", err
	}
	return renderChartManifestWithValues(loaded.Chart, userValues)
}

func renderChartManifestWithValues(source *helmchart.Chart, userValues map[string]interface{}) (string, error) {
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

	renderedFiles, err := engine.Render(chrt, renderValues)
	if err != nil {
		return "", err
	}

	removeNotesTemplates(renderedFiles)

	hooks, manifests, err := releaseutil.SortManifests(renderedFiles, nil, releaseutil.InstallOrder)
	if err != nil {
		return renderDebugManifest(renderedFiles), fmt.Errorf("sort manifests: %w", err)
	}

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
	return out.String(), nil
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
