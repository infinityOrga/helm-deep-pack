package pull

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	"gopkg.in/yaml.v3"
)

type searchResult struct {
	Version string `yaml:"version"`
}

type repoLookupKind string

const (
	repoLookupKindMissingChart   repoLookupKind = "missing-chart"
	repoLookupKindVersionMissing repoLookupKind = "version-missing"
)

type repoLookupError struct {
	kind         repoLookupKind
	repo         string
	chart        string
	requestedVer string
	charts       []string
	versions     []string
	repositories []repositoryCharts
	failures     []string
}

type repositoryCharts struct {
	name   string
	url    string
	charts []string
}

func (e *repoLookupError) Error() string {
	switch e.kind {
	case repoLookupKindMissingChart:
		var message strings.Builder
		if len(e.failures) > 0 {
			fmt.Fprintf(&message, "could not confirm whether chart %q is available", e.chart)
		} else {
			fmt.Fprintf(&message, "chart %q was not found", e.chart)
		}
		if suggestions := suggestChartNames(e.chart, e.availableNames()); len(suggestions) > 0 {
			message.WriteString("\nDid you mean:\n")
			for _, suggestion := range suggestions {
				fmt.Fprintf(&message, "  %s\n", suggestion)
			}
		}
		if len(e.repositories) > 0 {
			message.WriteString("Available charts by repository:\n")
			for _, repository := range e.repositories {
				label := repository.name
				if repository.url != "" && repository.url != repository.name {
					label += " (" + repository.url + ")"
				}
				fmt.Fprintf(&message, "  %s:\n", label)
				if len(repository.charts) == 0 {
					message.WriteString("    (no chart names listed)\n")
				}
				for _, name := range repository.charts {
					fmt.Fprintf(&message, "    %s\n", name)
				}
			}
		}
		if len(e.failures) > 0 {
			message.WriteString("Repository lookup failures:\n")
			for _, failure := range e.failures {
				fmt.Fprintf(&message, "  %s\n", failure)
			}
		}
		return strings.TrimRight(message.String(), "\n")
	case repoLookupKindVersionMissing:
		return fmt.Sprintf("chart %q found in repo %q, version %q missing; versions: %s", e.chart, e.repo, e.requestedVer, strings.Join(e.versions, ", "))
	default:
		return "repository lookup failed"
	}
}

func (e *repoLookupError) availableNames() []string {
	names := make(map[string]struct{}, len(e.charts))
	for _, name := range e.charts {
		names[name] = struct{}{}
	}
	for _, repository := range e.repositories {
		for _, name := range repository.charts {
			names[name] = struct{}{}
		}
	}
	return sortedKeys(names)
}

func suggestChartNames(query string, names []string) []string {
	query = strings.ToLower(query)
	limit := len(query) / 3
	if limit < 2 {
		limit = 2
	}
	type match struct {
		name     string
		distance int
	}
	matches := make([]match, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		distance := editDistance(query, strings.ToLower(name))
		if distance <= limit {
			matches = append(matches, match{name: name, distance: distance})
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].distance == matches[j].distance {
			return matches[i].name < matches[j].name
		}
		return matches[i].distance < matches[j].distance
	})
	if len(matches) > 3 {
		matches = matches[:3]
	}
	out := make([]string, 0, len(matches))
	for _, candidate := range matches {
		out = append(out, candidate.name)
	}
	return out
}

func editDistance(a, b string) int {
	previous := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i, left := range a {
		current := make([]int, len(b)+1)
		current[0] = i + 1
		for j, right := range b {
			cost := 0
			if left != right {
				cost = 1
			}
			current[j+1] = min(current[j]+1, previous[j+1]+1, previous[j]+cost)
		}
		previous = current
	}
	return previous[len(b)]
}

func loadRepoIndex(ctx context.Context, repoURL string) (map[string][]searchResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(repoURL, "/")+"/index.yaml", nil)
	if err != nil {
		return nil, fmt.Errorf("prepare chart index request: %w", err)
	}
	resp, err := outboundHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch chart index: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		if looksLikeNonHelmRepo(resp.Header.Get("Content-Type"), body) {
			return nil, fmt.Errorf("repo %q does not look like a Helm repository", repoURL)
		}
		return nil, fmt.Errorf("fetch chart index: %s: %s", resp.Status, string(body))
	}

	var index struct {
		APIVersion string                    `yaml:"apiVersion"`
		Entries    map[string][]searchResult `yaml:"entries"`
	}
	if err := yaml.NewDecoder(resp.Body).Decode(&index); err != nil {
		return nil, fmt.Errorf("repo %q does not look like a Helm repository", repoURL)
	}
	if index.APIVersion != "v1" {
		return nil, fmt.Errorf("repo %q does not look like a Helm repository", repoURL)
	}
	return index.Entries, nil
}

func looksLikeNonHelmRepo(contentType string, body []byte) bool {
	lower := strings.ToLower(contentType + "\n" + string(body))
	return strings.Contains(lower, "text/html") ||
		strings.Contains(lower, "<html") ||
		strings.Contains(lower, "<!doctype") ||
		strings.Contains(lower, "example domain")
}

func selectStableVersion(results []searchResult) (string, error) {
	for _, result := range results {
		if isStableVersion(result.Version) {
			return result.Version, nil
		}
	}
	return "", fmt.Errorf("no stable version found")
}

func versionExists(results []searchResult, version string) bool {
	for _, result := range results {
		if result.Version == version {
			return true
		}
	}
	return false
}

func availableChartNames(index map[string][]searchResult) []string {
	names := make([]string, 0, len(index))
	for name := range index {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func availableStableVersions(results []searchResult) []string {
	type stableVersion struct {
		raw string
		sem *semver.Version
	}
	versions := make([]stableVersion, 0, len(results))
	for _, result := range results {
		if !isStableVersion(result.Version) {
			continue
		}
		parsed, err := semver.NewVersion(result.Version)
		if err != nil {
			continue
		}
		versions = append(versions, stableVersion{raw: result.Version, sem: parsed})
	}
	sort.SliceStable(versions, func(i, j int) bool {
		return versions[i].sem.GreaterThan(versions[j].sem)
	})
	if len(versions) > 3 {
		versions = versions[:3]
	}
	out := make([]string, 0, len(versions))
	for _, version := range versions {
		out = append(out, version.raw)
	}
	return out
}

func isStableVersion(version string) bool {
	return version != "" && !strings.Contains(version, "-")
}
