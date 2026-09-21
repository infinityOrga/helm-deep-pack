package upgrade

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/Masterminds/semver/v3"
)

const (
	updateCheckInterval = 7 * 24 * time.Hour
	updateCheckTimeout  = 2 * time.Second
)

type UpdateCheckOptions struct {
	ReleaseLookupOptions
	CachePath string
	Now       func() time.Time
}

type UpdateNotice struct {
	CurrentVersion string
	LatestVersion  string
}

type updateCheckCache struct {
	CheckedAt     time.Time `json:"checked_at"`
	LatestVersion string    `json:"latest_version,omitempty"`
	NotifiedAt    time.Time `json:"notified_at"`
	CheckComplete bool      `json:"check_complete"`
}

func CheckForUpdate(ctx context.Context, opts UpdateCheckOptions) (UpdateNotice, error) {
	_, currentVersion, currentKnown := normalizeCurrentVersion(opts.CurrentVersion)
	if !currentKnown {
		return UpdateNotice{}, nil
	}

	now := time.Now()
	if opts.Now != nil {
		now = opts.Now()
	}

	cachePath := opts.CachePath
	if cachePath == "" {
		var err error
		cachePath, err = defaultUpdateCheckCachePath()
		if err != nil {
			return UpdateNotice{}, err
		}
	}

	cache, err := readUpdateCheckCache(cachePath)
	if err != nil {
		return UpdateNotice{}, err
	}
	if !cache.CheckedAt.IsZero() && now.Sub(cache.CheckedAt) < updateCheckInterval {
		return noticeFromCache(cache, currentVersion, now, cachePath)
	}

	lookup := opts.ReleaseLookupOptions
	if lookup.HTTPClient == nil {
		lookup.HTTPClient = &http.Client{Timeout: updateCheckTimeout}
	}
	checkCtx, cancel := context.WithTimeout(ctx, updateCheckTimeout)
	defer cancel()

	release, err := LookupRelease(checkCtx, lookup, "")
	if err != nil {
		cache.CheckedAt = now
		cache.CheckComplete = true
		_ = writeUpdateCheckCache(cachePath, cache)
		return UpdateNotice{}, err
	}

	previousLatest := cache.LatestVersion
	cache.CheckedAt = now
	cache.LatestVersion = release.BareVersion
	cache.CheckComplete = true
	if isNewerVersion(currentVersion, cache.LatestVersion) &&
		(previousLatest != cache.LatestVersion || notificationDue(cache.NotifiedAt, now)) {
		return persistUpdateNotice(cache, currentVersion, now, cachePath)
	}
	if err := writeUpdateCheckCache(cachePath, cache); err != nil {
		return UpdateNotice{}, err
	}
	return UpdateNotice{}, nil
}

func defaultUpdateCheckCachePath() (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve update check cache directory: %w", err)
	}
	return filepath.Join(cacheDir, "helm-deep-pack", "update-check.json"), nil
}

func readUpdateCheckCache(path string) (updateCheckCache, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return updateCheckCache{}, nil
	}
	if err != nil {
		return updateCheckCache{}, fmt.Errorf("read update check cache: %w", err)
	}

	var cache updateCheckCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return updateCheckCache{}, nil
	}
	if cache.CheckedAt.IsZero() || !cache.CheckComplete {
		return updateCheckCache{}, nil
	}
	if cache.LatestVersion != "" && !isSemver(cache.LatestVersion) {
		return updateCheckCache{}, nil
	}
	return cache, nil
}

func writeUpdateCheckCache(path string, cache updateCheckCache) error {
	data, err := json.Marshal(cache)
	if err != nil {
		return fmt.Errorf("encode update check cache: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create update check cache directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write update check cache: %w", err)
	}
	return nil
}

func noticeFromCache(cache updateCheckCache, currentVersion string, now time.Time, cachePath string) (UpdateNotice, error) {
	if !isNewerVersion(currentVersion, cache.LatestVersion) || !notificationDue(cache.NotifiedAt, now) {
		return UpdateNotice{}, nil
	}

	return persistUpdateNotice(cache, currentVersion, now, cachePath)
}

func persistUpdateNotice(cache updateCheckCache, currentVersion string, now time.Time, cachePath string) (UpdateNotice, error) {
	cache.NotifiedAt = now
	notice := UpdateNotice{
		CurrentVersion: currentVersion,
		LatestVersion:  cache.LatestVersion,
	}
	if err := writeUpdateCheckCache(cachePath, cache); err != nil {
		return notice, err
	}
	return notice, nil
}

func notificationDue(last time.Time, now time.Time) bool {
	return last.IsZero() || now.Sub(last) >= updateCheckInterval
}

func isNewerVersion(current, latest string) bool {
	currentVersion, err := semver.NewVersion(current)
	if err != nil {
		return false
	}
	latestVersion, err := semver.NewVersion(latest)
	if err != nil {
		return false
	}
	return latestVersion.GreaterThan(currentVersion)
}

func isSemver(version string) bool {
	_, err := semver.NewVersion(version)
	return err == nil
}
