package upgrade

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCheckForUpdateReportsNewerRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/helm-deep-pack/releases/latest" {
			t.Fatalf("request path = %q, want latest release path", r.URL.Path)
		}
		_, _ = fmt.Fprint(w, `{"tag_name":"v1.3.0"}`)
	}))
	defer server.Close()

	now := time.Date(2026, time.September, 21, 12, 0, 0, 0, time.UTC)
	notice, err := CheckForUpdate(context.Background(), UpdateCheckOptions{
		ReleaseLookupOptions: ReleaseLookupOptions{
			Owner:          "acme",
			Repo:           "helm-deep-pack",
			BaseURL:        server.URL,
			CurrentVersion: "1.2.0",
			HTTPClient:     server.Client(),
		},
		CachePath: filepath.Join(t.TempDir(), "update-check.json"),
		Now:       func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("CheckForUpdate() error = %v", err)
	}
	if notice.CurrentVersion != "1.2.0" {
		t.Fatalf("notice current version = %q, want %q", notice.CurrentVersion, "1.2.0")
	}
	if notice.LatestVersion != "1.3.0" {
		t.Fatalf("notice latest version = %q, want %q", notice.LatestVersion, "1.3.0")
	}
}

func TestCheckForUpdateDoesNotReportCurrentRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"tag_name":"v1.2.0"}`)
	}))
	defer server.Close()

	notice, err := CheckForUpdate(context.Background(), UpdateCheckOptions{
		ReleaseLookupOptions: ReleaseLookupOptions{
			Owner:          "acme",
			Repo:           "helm-deep-pack",
			BaseURL:        server.URL,
			CurrentVersion: "1.2.0",
			HTTPClient:     server.Client(),
		},
		CachePath: filepath.Join(t.TempDir(), "update-check.json"),
	})
	if err != nil {
		t.Fatalf("CheckForUpdate() error = %v", err)
	}
	if notice != (UpdateNotice{}) {
		t.Fatalf("notice = %#v, want empty", notice)
	}
}

func TestCheckForUpdateChecksAndRemindsWeekly(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_, _ = fmt.Fprint(w, `{"tag_name":"v1.3.0"}`)
	}))
	defer server.Close()

	now := time.Date(2026, time.September, 21, 12, 0, 0, 0, time.UTC)
	cachePath := filepath.Join(t.TempDir(), "update-check.json")
	check := func() UpdateNotice {
		notice, err := CheckForUpdate(context.Background(), UpdateCheckOptions{
			ReleaseLookupOptions: ReleaseLookupOptions{
				Owner:          "acme",
				Repo:           "helm-deep-pack",
				BaseURL:        server.URL,
				CurrentVersion: "1.2.0",
				HTTPClient:     server.Client(),
			},
			CachePath: cachePath,
			Now:       func() time.Time { return now },
		})
		if err != nil {
			t.Fatalf("CheckForUpdate() error = %v", err)
		}
		return notice
	}

	if notice := check(); notice.LatestVersion != "1.3.0" {
		t.Fatalf("first notice latest version = %q, want %q", notice.LatestVersion, "1.3.0")
	}
	now = now.Add(24 * time.Hour)
	if notice := check(); notice != (UpdateNotice{}) {
		t.Fatalf("daily notice = %#v, want empty", notice)
	}
	if requests != 1 {
		t.Fatalf("requests after daily check = %d, want 1", requests)
	}

	now = now.Add(6 * 24 * time.Hour)
	if notice := check(); notice.LatestVersion != "1.3.0" {
		t.Fatalf("weekly notice latest version = %q, want %q", notice.LatestVersion, "1.3.0")
	}
	if requests != 2 {
		t.Fatalf("requests after weekly check = %d, want 2", requests)
	}
}

func TestCheckForUpdateSkipsUnknownBuildVersion(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	defer server.Close()

	notice, err := CheckForUpdate(context.Background(), UpdateCheckOptions{
		ReleaseLookupOptions: ReleaseLookupOptions{
			BaseURL:        server.URL,
			CurrentVersion: "dev",
			HTTPClient:     server.Client(),
		},
		CachePath: filepath.Join(t.TempDir(), "update-check.json"),
	})
	if err != nil {
		t.Fatalf("CheckForUpdate() error = %v", err)
	}
	if notice != (UpdateNotice{}) {
		t.Fatalf("notice = %#v, want empty", notice)
	}
	if called {
		t.Fatal("update endpoint was called for dev version")
	}
}

func TestCheckForUpdateRecoversFromCorruptCache(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "update-check.json")
	if err := os.WriteFile(cachePath, []byte("not json"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"tag_name":"v1.3.0"}`)
	}))
	defer server.Close()

	notice, err := CheckForUpdate(context.Background(), UpdateCheckOptions{
		ReleaseLookupOptions: ReleaseLookupOptions{
			Owner:          "acme",
			Repo:           "helm-deep-pack",
			BaseURL:        server.URL,
			CurrentVersion: "1.2.0",
			HTTPClient:     server.Client(),
		},
		CachePath: cachePath,
	})
	if err != nil {
		t.Fatalf("CheckForUpdate() error = %v", err)
	}
	if notice.LatestVersion != "1.3.0" {
		t.Fatalf("notice latest version = %q, want %q", notice.LatestVersion, "1.3.0")
	}
}

func TestCheckForUpdateThrottlesFailedChecks(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	now := time.Date(2026, time.September, 21, 12, 0, 0, 0, time.UTC)
	cachePath := filepath.Join(t.TempDir(), "update-check.json")
	options := func() UpdateCheckOptions {
		return UpdateCheckOptions{
			ReleaseLookupOptions: ReleaseLookupOptions{
				Owner:          "acme",
				Repo:           "helm-deep-pack",
				BaseURL:        server.URL,
				CurrentVersion: "1.2.0",
				HTTPClient:     server.Client(),
			},
			CachePath: cachePath,
			Now:       func() time.Time { return now },
		}
	}

	if _, err := CheckForUpdate(context.Background(), options()); err == nil {
		t.Fatal("CheckForUpdate() error = nil, want request failure")
	}
	if _, err := CheckForUpdate(context.Background(), options()); err != nil {
		t.Fatalf("cached failed CheckForUpdate() error = %v", err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
}
