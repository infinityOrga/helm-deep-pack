package upgrade

import (
	"context"
	"net/http"
)

func DefaultReleaseLookupOptions(currentVersion string) ReleaseLookupOptions {
	owner, repo := defaultReleaseTarget(currentVersion)
	return ReleaseLookupOptions{
		Owner:          owner,
		Repo:           repo,
		BaseURL:        releaseBaseURL,
		CurrentVersion: currentVersion,
	}
}

type ReleaseLookupOptions struct {
	Owner          string
	Repo           string
	BaseURL        string
	CurrentVersion string
	HTTPClient     *http.Client
}

type ReleaseAsset struct {
	Name string
	URL  string
	Size int64
}

type ReleaseMetadata struct {
	TagName     string
	BareVersion string
	Assets      []ReleaseAsset
}

func LookupRelease(ctx context.Context, opts ReleaseLookupOptions, targetVersion string) (ReleaseMetadata, error) {
	upgradeOpts := applyDefaults(Options{
		Owner:          opts.Owner,
		Repo:           opts.Repo,
		BaseURL:        opts.BaseURL,
		CurrentVersion: opts.CurrentVersion,
		HTTPClient:     opts.HTTPClient,
		TargetVersion:  targetVersion,
	})
	info, tag, bare, err := resolveRelease(ctx, upgradeOpts)
	if err != nil {
		return ReleaseMetadata{}, err
	}
	assets := make([]ReleaseAsset, 0, len(info.Assets))
	for _, asset := range info.Assets {
		assets = append(assets, ReleaseAsset(asset))
	}
	return ReleaseMetadata{TagName: tag, BareVersion: bare, Assets: assets}, nil
}

func FetchReleaseAssetBytes(ctx context.Context, opts ReleaseLookupOptions, asset ReleaseAsset) ([]byte, error) {
	upgradeOpts := applyDefaults(Options{
		Owner:          opts.Owner,
		Repo:           opts.Repo,
		BaseURL:        opts.BaseURL,
		CurrentVersion: opts.CurrentVersion,
		HTTPClient:     opts.HTTPClient,
	})
	return fetchAssetBytes(ctx, upgradeOpts, releaseAsset(asset))
}

func ParseChecksumsText(content string) (map[string]string, error) {
	return parseChecksums(content)
}
