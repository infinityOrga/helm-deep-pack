package pull

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMissingChartErrorSuggestsAndListsEveryRepository(t *testing.T) {
	err := (&repoLookupError{
		kind:  repoLookupKindMissingChart,
		chart: "ngnix",
		repositories: []repositoryCharts{
			{name: "stable", url: "https://charts.example/stable", charts: []string{"nginx", "redis"}},
			{name: "internal", url: "https://charts.example/internal", charts: []string{"mychart"}},
		},
		failures: []string{"broken (https://charts.example/broken): index request timed out"},
	}).Error()
	for _, want := range []string{
		`could not confirm whether chart "ngnix" is available`,
		"Did you mean:\n  nginx",
		"stable (https://charts.example/stable):\n    nginx\n    redis",
		"internal (https://charts.example/internal):\n    mychart",
		"Repository lookup failures:\n  broken (https://charts.example/broken): index request timed out",
	} {
		if !strings.Contains(err, want) {
			t.Errorf("error missing %q:\n%s", want, err)
		}
	}
}

func TestConfiguredRepoFailuresDoNotClaimChartIsMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	repoConfig := filepath.Join(t.TempDir(), "repositories.yaml")
	content := fmt.Sprintf("apiVersion: v1\nrepositories:\n- name: offline\n  url: %s\n", server.URL)
	if err := os.WriteFile(repoConfig, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	t.Setenv("HELM_REPOSITORY_CONFIG", repoConfig)

	_, err := NewRunner().loadChartFromConfiguredRepos(context.Background(), Options{Chart: "ngnix"})
	if err == nil {
		t.Fatal("loadChartFromConfiguredRepos() error = nil, want index lookup failure")
	}
	if !strings.Contains(err.Error(), `could not confirm whether chart "ngnix" is available`) {
		t.Fatalf("error should report indeterminate lookup, got: %v", err)
	}
	if strings.Contains(err.Error(), `chart "ngnix" was not found`) {
		t.Fatalf("error incorrectly claims chart is missing: %v", err)
	}
}

func TestSuggestChartNamesReturnsClosestThree(t *testing.T) {
	got := suggestChartNames("ngin", []string{"nginx", "ngina", "nginu", "ngini", "redis"})
	want := []string{"ngina", "ngini", "nginu"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("suggestChartNames() = %v, want %v", got, want)
	}
}
