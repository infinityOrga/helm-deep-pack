package pull

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mitchellh/copystructure"
	"helm.sh/helm/v3/pkg/chartutil"
)

type optionalImageDiscovery struct {
	Images        []string
	OptionalFlags map[string][]string
	Warnings      []string
}

type valuePathPart struct {
	key   string
	index int
	isKey bool
}

type valuePath []valuePathPart

func (p valuePath) String() string {
	var b strings.Builder
	b.WriteString(".Values")
	for _, part := range p {
		if part.isKey {
			b.WriteByte('.')
			b.WriteString(part.key)
			continue
		}
		fmt.Fprintf(&b, "[%d]", part.index)
	}
	return b.String()
}

func (r Runner) discoverOptionalChartImages(ctx context.Context, opts Options, baseline []string) (optionalImageDiscovery, error) {
	if opts.RenderedOnly {
		return optionalImageDiscovery{}, nil
	}

	loaded, err := r.loadChart(ctx, opts)
	if err != nil {
		return optionalImageDiscovery{}, err
	}
	userValues, err := renderUserValues(opts)
	if err != nil {
		return optionalImageDiscovery{}, err
	}
	coalescedValues, err := chartutil.CoalesceValues(loaded.Chart, userValues)
	if err != nil {
		return optionalImageDiscovery{}, fmt.Errorf("coalesce chart values: %w", err)
	}
	effectiveValues := map[string]interface{}(coalescedValues)

	falsePaths := collectFalseBooleanPaths(effectiveValues)
	if len(falsePaths) == 0 {
		return optionalImageDiscovery{}, nil
	}

	optionalValues, err := cloneValues(effectiveValues)
	if err != nil {
		return optionalImageDiscovery{}, err
	}
	enableValuePaths(optionalValues, falsePaths)

	syntheticManifest, err := r.renderManifestValues(r, ctx, opts, optionalValues)
	if err != nil {
		return optionalImageDiscovery{
			Warnings: []string{fmt.Sprintf("optional image discovery render failed; optional images may be missing (discover or add them explicitly): %v", err)},
		}, nil
	}
	syntheticImages, err := r.extractImages(syntheticManifest)
	if err != nil {
		return optionalImageDiscovery{
			Warnings: []string{fmt.Sprintf("optional image discovery extraction failed; optional images may be missing (discover or add them explicitly): %v", err)},
		}, nil
	}

	baselineSet := make(map[string]struct{}, len(baseline))
	for _, image := range baseline {
		baselineSet[image] = struct{}{}
	}
	optional := make([]string, 0, len(syntheticImages))
	optionalSet := make(map[string]struct{}, len(syntheticImages))
	for _, image := range syntheticImages {
		if _, ok := baselineSet[image]; ok {
			continue
		}
		if _, ok := optionalSet[image]; ok {
			continue
		}
		optionalSet[image] = struct{}{}
		optional = append(optional, image)
	}

	discovery := optionalImageDiscovery{Images: optional, OptionalFlags: make(map[string][]string)}
	if len(optional) == 0 || opts.OptionalImageTimeout <= 0 {
		return discovery, nil
	}

	flags, warnings := r.attributeOptionalImages(ctx, opts, effectiveValues, falsePaths, baselineSet, optionalSet)
	discovery.OptionalFlags = flags
	discovery.Warnings = append(discovery.Warnings, warnings...)
	return discovery, nil
}

func cloneValues(values map[string]interface{}) (map[string]interface{}, error) {
	copyValue, err := copystructure.Copy(values)
	if err != nil {
		return nil, fmt.Errorf("clone chart values: %w", err)
	}
	cloned, ok := copyValue.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("clone chart values: unexpected copy type %T", copyValue)
	}
	return cloned, nil
}

func collectFalseBooleanPaths(values interface{}) []valuePath {
	var paths []valuePath
	collectFalseBooleanPathsAt(values, nil, &paths)
	return paths
}

func collectFalseBooleanPathsAt(value interface{}, path valuePath, paths *[]valuePath) {
	switch typed := value.(type) {
	case map[string]interface{}:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			childPath := appendPathKey(path, key)
			if flag, ok := typed[key].(bool); ok {
				if !flag {
					*paths = append(*paths, childPath)
				}
				continue
			}
			collectFalseBooleanPathsAt(typed[key], childPath, paths)
		}
	case []interface{}:
		for index, child := range typed {
			childPath := appendPathIndex(path, index)
			collectFalseBooleanPathsAt(child, childPath, paths)
		}
	}
}

func appendPathKey(path valuePath, key string) valuePath {
	result := append(valuePath(nil), path...)
	return append(result, valuePathPart{key: key, isKey: true})
}

func appendPathIndex(path valuePath, index int) valuePath {
	result := append(valuePath(nil), path...)
	return append(result, valuePathPart{index: index})
}

func enableValuePaths(values map[string]interface{}, paths []valuePath) {
	for _, path := range paths {
		var current interface{} = values
		for index, part := range path {
			last := index == len(path)-1
			switch typed := current.(type) {
			case map[string]interface{}:
				if last {
					typed[part.key] = true
					continue
				}
				current = typed[part.key]
			case []interface{}:
				if part.isKey || part.index < 0 || part.index >= len(typed) {
					current = nil
					continue
				}
				if last {
					typed[part.index] = true
					continue
				}
				current = typed[part.index]
			default:
				current = nil
			}
		}
	}
}

func (r Runner) attributeOptionalImages(
	ctx context.Context,
	opts Options,
	baseValues map[string]interface{},
	falsePaths []valuePath,
	baselineSet, optionalSet map[string]struct{},
) (map[string][]string, []string) {
	deadline := time.Now().Add(opts.OptionalImageTimeout)
	flagsByImage := make(map[string][]string)
	cache := make(map[string]map[string]struct{})
	warnings := make([]string, 0)
	probeFailed := false

	allKey := valuePathsKey(falsePaths)
	cache[allKey] = imageSetFromMap(optionalSet)

	probe := func(paths []valuePath) (map[string]struct{}, bool) {
		if ctx.Err() != nil || time.Now().After(deadline) {
			return nil, false
		}
		key := valuePathsKey(paths)
		if images, ok := cache[key]; ok {
			return images, true
		}
		values, err := cloneValues(baseValues)
		if err != nil {
			probeFailed = true
			return nil, false
		}
		enableValuePaths(values, paths)
		manifest, err := r.renderManifestValues(r, ctx, opts, values)
		if err != nil {
			if ctx.Err() != nil || time.Now().After(deadline) {
				return nil, false
			}
			probeFailed = true
			return nil, false
		}
		extracted, err := r.extractImages(manifest)
		if err != nil {
			probeFailed = true
			return nil, false
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			return nil, false
		}
		images := make(map[string]struct{}, len(extracted))
		for _, image := range extracted {
			if _, isBaseline := baselineSet[image]; isBaseline {
				continue
			}
			if _, isOptional := optionalSet[image]; isOptional {
				images[image] = struct{}{}
			}
		}
		cache[key] = images
		return images, true
	}

	unresolved := make(map[string]struct{}, len(optionalSet))
	for image := range optionalSet {
		unresolved[image] = struct{}{}
	}
	for _, path := range falsePaths {
		images, ok := probe([]valuePath{path})
		if !ok {
			break
		}
		for image := range images {
			flagsByImage[image] = appendUnique(flagsByImage[image], path.String())
			delete(unresolved, image)
		}
	}

	for image := range unresolved {
		if len(falsePaths) < 2 || time.Now().After(deadline) {
			break
		}
		minimal, complete := minimalImageFlagSet(image, falsePaths, probe, deadline)
		if !complete {
			continue
		}
		for _, path := range minimal {
			flagsByImage[image] = appendUnique(flagsByImage[image], path.String())
		}
	}

	if time.Now().After(deadline) {
		warnings = append(warnings, fmt.Sprintf("optional image flag attribution exceeded %s; some optional images may not show flag paths", opts.OptionalImageTimeout))
	}
	if probeFailed {
		warnings = append(warnings, "some optional image flag attribution probes failed; affected images remain marked optional")
	}
	return flagsByImage, warnings
}

func imageSetFromMap(values map[string]struct{}) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for value := range values {
		result[value] = struct{}{}
	}
	return result
}

func minimalImageFlagSet(image string, paths []valuePath, probe func([]valuePath) (map[string]struct{}, bool), deadline time.Time) ([]valuePath, bool) {
	active := append([]valuePath(nil), paths...)
	granularity := 2
	for len(active) > 1 && time.Now().Before(deadline) {
		if granularity > len(active) {
			granularity = len(active)
		}
		chunks := splitValuePaths(active, granularity)
		reduced := false
		for _, chunk := range chunks {
			candidate := subtractValuePaths(active, chunk)
			if len(candidate) == 0 {
				continue
			}
			images, ok := probe(candidate)
			if !ok {
				return nil, false
			}
			if _, present := images[image]; present {
				active = candidate
				reduced = true
				break
			}
		}
		if reduced {
			if granularity > 2 {
				granularity--
			}
			continue
		}
		if granularity == len(active) {
			break
		}
		granularity *= 2
	}
	if !time.Now().Before(deadline) {
		return nil, false
	}
	return active, true
}

func splitValuePaths(paths []valuePath, count int) [][]valuePath {
	if count < 1 {
		count = 1
	}
	result := make([][]valuePath, 0, count)
	for i := 0; i < count && len(paths) > 0; i++ {
		start := i * len(paths) / count
		end := (i + 1) * len(paths) / count
		if end <= start {
			continue
		}
		result = append(result, paths[start:end])
	}
	return result
}

func subtractValuePaths(paths, remove []valuePath) []valuePath {
	removeSet := make(map[string]struct{}, len(remove))
	for _, path := range remove {
		removeSet[pathKey(path)] = struct{}{}
	}
	result := make([]valuePath, 0, len(paths)-len(remove))
	for _, path := range paths {
		if _, ok := removeSet[pathKey(path)]; !ok {
			result = append(result, path)
		}
	}
	return result
}

func valuePathsKey(paths []valuePath) string {
	keys := make([]string, 0, len(paths))
	for _, path := range paths {
		keys = append(keys, pathKey(path))
	}
	sort.Strings(keys)
	return strings.Join(keys, "\x1f")
}

func pathKey(path valuePath) string {
	return path.String()
}
