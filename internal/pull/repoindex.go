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
}

func (e *repoLookupError) Error() string {
	switch e.kind {
	case repoLookupKindMissingChart:
		return fmt.Sprintf("chart %q not found in repo %q; charts: %s", e.chart, e.repo, strings.Join(e.charts, ", "))
	case repoLookupKindVersionMissing:
		return fmt.Sprintf("chart %q found in repo %q, version %q missing; versions: %s", e.chart, e.repo, e.requestedVer, strings.Join(e.versions, ", "))
	default:
		return "repository lookup failed"
	}
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
