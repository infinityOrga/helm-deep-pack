package upgrade

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
)

const (
	defaultReleaseBaseURL  = "https://api.github.com"
	maxExtractedBinarySize = 128 << 20
)

var (
	releaseBaseURL = defaultReleaseBaseURL
)

var (
	resolveExecutablePath = os.Executable
	evalSymlinks          = filepath.EvalSymlinks
	runtimeGOOS           = runtime.GOOS
	runtimeGOARCH         = runtime.GOARCH
	readGitRemoteOrigin   = func() (string, error) {
		output, err := exec.Command("git", "config", "--get", "remote.origin.url").Output()
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(output)), nil
	}
)

type Options struct {
	CurrentVersion string
	TargetVersion  string
	Force          bool
	AssumeYes      bool
	Owner          string
	Repo           string
	BaseURL        string
	HTTPClient     *http.Client
	In             io.Reader
	Out            io.Writer
	Logger         *slog.Logger
}

type release struct {
	TagName string
	Assets  []releaseAsset
}

type releaseAsset struct {
	Name string
	URL  string
	Size int64
}

type ghRelease struct {
	TagName    string    `json:"tag_name"`
	Prerelease bool      `json:"prerelease"`
	Draft      bool      `json:"draft"`
	Assets     []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

func Run(ctx context.Context, opts Options, status ...io.Writer) error {
	opts = applyDefaults(opts)
	executablePath, err := resolveExecutable()
	if err != nil {
		return err
	}

	releaseInfo, targetTag, targetBare, err := resolveRelease(ctx, opts)
	if err != nil {
		return err
	}

	currentTag, currentBare, currentKnown := normalizeCurrentVersion(opts.CurrentVersion)
	if currentKnown && currentBare == targetBare && !opts.Force {
		_, writeErr := fmt.Fprintf(opts.Out, "already up to date (%s)\n", targetTag)
		if writeErr != nil {
			return fmt.Errorf("write output: %w", writeErr)
		}
		return nil
	}

	archiveAssetName := assetName(targetBare, runtimeGOOS, runtimeGOARCH)
	archiveAsset, ok := findAsset(releaseInfo.Assets, archiveAssetName)
	if !ok {
		return fmt.Errorf("release %s does not include required asset %q (available: %s)", targetTag, archiveAssetName, strings.Join(assetNames(releaseInfo.Assets), ", "))
	}
	checksumAsset, ok := findAsset(releaseInfo.Assets, "checksums.txt")
	if !ok {
		return fmt.Errorf("release %s is missing checksums.txt", targetTag)
	}

	if !opts.AssumeYes {
		accepted, promptErr := promptUpgrade(opts, currentTag, targetTag)
		if promptErr != nil {
			return promptErr
		}
		if !accepted {
			_, writeErr := fmt.Fprintln(opts.Out, "upgrade cancelled")
			if writeErr != nil {
				return fmt.Errorf("write output: %w", writeErr)
			}
			return nil
		}
	}

	checksums, err := fetchChecksums(ctx, opts, checksumAsset)
	if err != nil {
		return err
	}
	expectedHash, ok := checksums[archiveAssetName]
	if !ok {
		return fmt.Errorf("checksums.txt does not include %q", archiveAssetName)
	}

	executableDir := filepath.Dir(executablePath)
	downloadedArchivePath, err := downloadArchive(ctx, opts, archiveAsset, expectedHash, executableDir)
	if err != nil {
		return err
	}
	defer func() {
		_ = os.Remove(downloadedArchivePath)
	}()

	newBinaryPath, err := extractBinaryTemp(downloadedArchivePath, executableDir, runtimeGOOS)
	if err != nil {
		return err
	}
	if chmodErr := os.Chmod(newBinaryPath, 0o755); chmodErr != nil {
		_ = os.Remove(newBinaryPath)
		return fmt.Errorf("chmod extracted binary: %w", chmodErr)
	}

	scheduled, err := replaceExecutable(executablePath, newBinaryPath)
	if err != nil {
		_ = os.Remove(newBinaryPath)
		return err
	}

	message := fmt.Sprintf("upgraded helm-deep-pack %s -> %s\n", currentTag, targetTag)
	if scheduled {
		message = fmt.Sprintf("scheduled upgrade for helm-deep-pack %s -> %s\n", currentTag, targetTag)
	}
	_, err = fmt.Fprint(opts.Out, message)
	if err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	return nil
}

func applyDefaults(opts Options) Options {
	defaultOwner, defaultRepo := defaultReleaseTarget(opts.CurrentVersion)
	if strings.TrimSpace(opts.Owner) == "" {
		opts.Owner = defaultOwner
	}
	if strings.TrimSpace(opts.Repo) == "" {
		opts.Repo = defaultRepo
	}
	if strings.TrimSpace(opts.BaseURL) == "" {
		opts.BaseURL = releaseBaseURL
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: 60 * time.Second}
	}
	if opts.In == nil {
		opts.In = os.Stdin
	}
	if opts.Out == nil {
		opts.Out = io.Discard
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return opts
}

func defaultReleaseTarget(currentVersion string) (owner, repo string) {
	owner = "infinityOrga"
	repo = "helm-deep-pack"
	if !isDevVersion(currentVersion) {
		return owner, repo
	}
	gitRemote, err := readGitRemoteOrigin()
	if err != nil {
		return owner, repo
	}
	remoteOwner, remoteRepo, ok := parseGitRemote(gitRemote)
	if !ok {
		return owner, repo
	}
	return remoteOwner, remoteRepo
}

func parseGitRemote(raw string) (owner, repo string, ok bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", "", false
	}
	trimmed = strings.TrimSuffix(trimmed, ".git")

	if strings.Contains(trimmed, "@") && strings.Contains(trimmed, ":") && !strings.Contains(trimmed, "://") {
		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) != 2 {
			return "", "", false
		}
		return parseOwnerRepoPath(parts[1])
	}

	parsedURL, err := url.Parse(trimmed)
	if err != nil {
		return "", "", false
	}
	return parseOwnerRepoPath(strings.TrimPrefix(parsedURL.Path, "/"))
}

func parseOwnerRepoPath(path string) (owner, repo string, ok bool) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 2 {
		return "", "", false
	}
	if strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func isDevVersion(version string) bool {
	trimmed := strings.TrimSpace(version)
	return trimmed == "" || strings.EqualFold(trimmed, "dev")
}

func resolveExecutable() (string, error) {
	executablePath, err := resolveExecutablePath()
	if err != nil {
		return "", fmt.Errorf("resolve executable path: %w", err)
	}
	realPath, err := evalSymlinks(executablePath)
	if err != nil {
		realPath = executablePath
	}
	realPath, err = filepath.Abs(realPath)
	if err != nil {
		return "", fmt.Errorf("resolve executable absolute path: %w", err)
	}
	return realPath, nil
}

func resolveRelease(ctx context.Context, opts Options) (release, string, string, error) {
	if strings.TrimSpace(opts.TargetVersion) == "" {
		releaseInfo, err := fetchRelease(ctx, opts, "/repos/"+opts.Owner+"/"+opts.Repo+"/releases/latest")
		if err != nil {
			return release{}, "", "", err
		}
		tag, bare, err := normalizeTag(releaseInfo.TagName)
		if err != nil {
			return release{}, "", "", fmt.Errorf("parse latest release tag %q: %w", releaseInfo.TagName, err)
		}
		return releaseInfo, tag, bare, nil
	}

	tag, bare, err := normalizeTag(opts.TargetVersion)
	if err != nil {
		return release{}, "", "", fmt.Errorf("normalize --version: %w", err)
	}
	releaseInfo, err := fetchRelease(ctx, opts, "/repos/"+opts.Owner+"/"+opts.Repo+"/releases/tags/"+tag)
	if err != nil {
		return release{}, "", "", err
	}
	return releaseInfo, tag, bare, nil
}

func fetchRelease(ctx context.Context, opts Options, path string) (release, error) {
	url := strings.TrimRight(opts.BaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return release{}, fmt.Errorf("build github request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "helm-deep-pack/"+strings.TrimSpace(opts.CurrentVersion))

	resp, err := opts.HTTPClient.Do(req)
	if err != nil {
		return release{}, fmt.Errorf("request github release metadata: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if resp.StatusCode == http.StatusNotFound {
			return release{}, fmt.Errorf("release not found")
		}
		if resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0" {
			return release{}, fmt.Errorf("github rate limit reached, try again later")
		}
		return release{}, fmt.Errorf("github release metadata request failed: %s (%s)", resp.Status, strings.TrimSpace(string(body)))
	}

	var payload ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return release{}, fmt.Errorf("decode github release metadata: %w", err)
	}

	result := release{TagName: payload.TagName, Assets: make([]releaseAsset, 0, len(payload.Assets))}
	for _, asset := range payload.Assets {
		result.Assets = append(result.Assets, releaseAsset(asset))
	}
	return result, nil
}

func normalizeTag(input string) (string, string, error) {
	raw := strings.TrimSpace(input)
	if raw == "" {
		return "", "", fmt.Errorf("version cannot be empty")
	}
	raw = strings.TrimPrefix(raw, "v")
	v, err := semver.NewVersion(raw)
	if err != nil {
		return "", "", fmt.Errorf("invalid semantic version %q: %w", input, err)
	}
	bare := v.String()
	return "v" + bare, bare, nil
}

func normalizeCurrentVersion(version string) (string, string, bool) {
	if isDevVersion(version) {
		return "dev", "", false
	}
	tag, bare, err := normalizeTag(version)
	if err != nil {
		return version, "", false
	}
	return tag, bare, true
}

func assetName(bareVersion, goos, goarch string) string {
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("helm-deep-pack_%s_%s_%s.%s", bareVersion, goos, goarch, ext)
}

func binaryName(goos string) string {
	if goos == "windows" {
		return "helm-deep-pack.exe"
	}
	return "helm-deep-pack"
}

func findAsset(assets []releaseAsset, name string) (releaseAsset, bool) {
	for _, asset := range assets {
		if asset.Name == name {
			return asset, true
		}
	}
	return releaseAsset{}, false
}

func assetNames(assets []releaseAsset) []string {
	result := make([]string, 0, len(assets))
	for _, asset := range assets {
		result = append(result, asset.Name)
	}
	slices.Sort(result)
	return result
}

func promptUpgrade(opts Options, fromVersion, toVersion string) (bool, error) {
	_, err := fmt.Fprintf(opts.Out, "Upgrade helm-deep-pack %s -> %s for %s/%s? [y/N]: ", fromVersion, toVersion, runtimeGOOS, runtimeGOARCH)
	if err != nil {
		return false, fmt.Errorf("write prompt: %w", err)
	}
	reader := bufio.NewReader(opts.In)
	line, readErr := reader.ReadString('\n')
	if readErr != nil && readErr != io.EOF {
		return false, fmt.Errorf("read prompt input: %w", readErr)
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

func fetchChecksums(ctx context.Context, opts Options, asset releaseAsset) (map[string]string, error) {
	content, err := fetchAssetBytes(ctx, opts, asset)
	if err != nil {
		return nil, err
	}
	return parseChecksums(string(content))
}

func parseChecksums(content string) (map[string]string, error) {
	result := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(content))
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return nil, fmt.Errorf("invalid checksums line %d: %q", lineNo, line)
		}
		hash := strings.ToLower(fields[0])
		file := fields[len(fields)-1]
		result[file] = hash
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read checksums: %w", err)
	}
	return result, nil
}

func fetchAssetBytes(ctx context.Context, opts Options, asset releaseAsset) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("build asset request: %w", err)
	}
	req.Header.Set("User-Agent", "helm-deep-pack/"+strings.TrimSpace(opts.CurrentVersion))

	resp, err := opts.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", asset.Name, err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: unexpected status %s", asset.Name, resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", asset.Name, err)
	}
	return body, nil
}

func downloadArchive(ctx context.Context, opts Options, asset releaseAsset, expectedHash, destinationDir string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
	if err != nil {
		return "", fmt.Errorf("build archive request: %w", err)
	}
	req.Header.Set("User-Agent", "helm-deep-pack/"+strings.TrimSpace(opts.CurrentVersion))
	resp, err := opts.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("download archive: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download archive: unexpected status %s", resp.Status)
	}

	suffix := ".bin"
	if strings.HasSuffix(strings.ToLower(asset.Name), ".tar.gz") {
		suffix = ".tar.gz"
	} else if strings.HasSuffix(strings.ToLower(asset.Name), ".zip") {
		suffix = ".zip"
	}
	tmpFile, err := os.CreateTemp(destinationDir, ".helm-deep-pack-download-*"+suffix)
	if err != nil {
		return "", fmt.Errorf("create temporary archive: %w", err)
	}
	tmpPath := tmpFile.Name()

	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(tmpFile, hasher), resp.Body)
	closeErr := tmpFile.Close()
	if copyErr != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("write temporary archive: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("close temporary archive: %w", closeErr)
	}
	if asset.Size > 0 && written != asset.Size {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("download archive size mismatch: got %d bytes, expected %d", written, asset.Size)
	}
	actualHash := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(actualHash, expectedHash) {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("checksum mismatch for %s: expected %s, got %s", asset.Name, expectedHash, actualHash)
	}
	return tmpPath, nil
}

func extractBinaryTemp(archivePath, outputDir, goos string) (string, error) {
	tmpFile, err := os.CreateTemp(outputDir, ".helm-deep-pack-update-*")
	if err != nil {
		return "", fmt.Errorf("create temporary binary file: %w", err)
	}
	tmpPath := tmpFile.Name()
	if err := extractBinary(archivePath, goos, tmpFile); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("close temporary binary file: %w", err)
	}
	return tmpPath, nil
}

func extractBinary(archivePath, goos string, dst io.Writer) error {
	switch {
	case strings.HasSuffix(strings.ToLower(archivePath), ".zip"):
		return extractFromZip(archivePath, goos, dst)
	case strings.HasSuffix(strings.ToLower(archivePath), ".tar.gz"):
		return extractFromTarGz(archivePath, goos, dst)
	default:
		return fmt.Errorf("unsupported archive format: %s", archivePath)
	}
}

func extractFromTarGz(archivePath, goos string, dst io.Writer) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer func() {
		_ = f.Close()
	}()
	gzReader, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("open gzip stream: %w", err)
	}
	defer func() {
		_ = gzReader.Close()
	}()
	tr := tar.NewReader(gzReader)
	expected := binaryName(goos)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar archive: %w", err)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		if filepath.Base(header.Name) != expected {
			continue
		}
		if err := copyArchiveEntry(dst, tr, header.Size); err != nil {
			return fmt.Errorf("extract binary from tar archive: %w", err)
		}
		return nil
	}
	return fmt.Errorf("binary %q not found in archive", expected)
}

func extractFromZip(archivePath, goos string, dst io.Writer) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open zip archive: %w", err)
	}
	defer func() {
		_ = zr.Close()
	}()
	expected := binaryName(goos)
	for _, file := range zr.File {
		if filepath.Base(file.Name) != expected {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			return fmt.Errorf("open zip entry %q: %w", file.Name, err)
		}
		copyErr := copyArchiveEntry(dst, rc, int64(file.UncompressedSize64))
		closeErr := rc.Close()
		if copyErr != nil {
			return fmt.Errorf("extract binary from zip archive: %w", copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close zip entry %q: %w", file.Name, closeErr)
		}
		return nil
	}
	return fmt.Errorf("binary %q not found in archive", expected)
}

func copyArchiveEntry(dst io.Writer, src io.Reader, size int64) error {
	if size < 0 || size > maxExtractedBinarySize {
		return fmt.Errorf("archive entry is too large: %d bytes (maximum %d)", size, maxExtractedBinarySize)
	}
	_, err := io.CopyN(dst, src, size)
	return err
}

func replaceExecutable(realPath, newPath string) (bool, error) {
	if runtimeGOOS == "windows" {
		if err := launchWindowsHelper(realPath, newPath); err != nil {
			return false, err
		}
		return true, nil
	}
	if err := performExecutableSwap(realPath, newPath); err != nil {
		return false, err
	}
	return false, nil
}

func launchWindowsHelper(realPath, newPath string) error {
	helperPath, err := createWindowsHelperCopy()
	if err != nil {
		return err
	}
	// createWindowsHelperCopy creates helperPath from this executable in a new
	// temporary file; it is not derived from user input. StartProcess passes
	// the helper arguments directly without shell interpretation.
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		_ = os.Remove(helperPath)
		return fmt.Errorf("open helper output: %w", err)
	}
	defer func() { _ = devNull.Close() }()

	proc, err := os.StartProcess(helperPath,
		[]string{helperPath, "upgrade-helper", "--target-exe", realPath, "--incoming-exe", newPath},
		&os.ProcAttr{Files: []*os.File{devNull, devNull, devNull}},
	)
	if err != nil {
		_ = os.Remove(helperPath)
		return fmt.Errorf("start windows helper: %w", err)
	}
	_ = proc.Release()
	return nil
}

func createWindowsHelperCopy() (string, error) {
	src, err := resolveExecutablePath()
	if err != nil {
		return "", fmt.Errorf("resolve helper source executable: %w", err)
	}
	src, err = evalSymlinks(src)
	if err != nil {
		src = filepath.Clean(src)
	}
	input, err := os.Open(src)
	if err != nil {
		return "", fmt.Errorf("open helper source executable: %w", err)
	}
	defer func() { _ = input.Close() }()

	helperDir := filepath.Join(os.TempDir(), "helm-deep-pack-upgrade")
	if err := os.MkdirAll(helperDir, 0o755); err != nil {
		return "", fmt.Errorf("create helper directory: %w", err)
	}
	helperFile, err := os.CreateTemp(helperDir, "helper-*.exe")
	if err != nil {
		return "", fmt.Errorf("create helper copy: %w", err)
	}
	helperPath := helperFile.Name()
	if _, err := io.Copy(helperFile, input); err != nil {
		_ = helperFile.Close()
		_ = os.Remove(helperPath)
		return "", fmt.Errorf("copy helper executable: %w", err)
	}
	if err := helperFile.Close(); err != nil {
		_ = os.Remove(helperPath)
		return "", fmt.Errorf("close helper copy: %w", err)
	}
	return helperPath, nil
}

func performExecutableSwap(realPath, newPath string) error {
	oldPath := realPath + ".old"
	_ = os.Remove(oldPath)
	if err := os.Rename(realPath, oldPath); err != nil {
		return fmt.Errorf("move current executable aside: %w", err)
	}
	if err := os.Rename(newPath, realPath); err != nil {
		rollbackErr := os.Rename(oldPath, realPath)
		if rollbackErr != nil {
			return fmt.Errorf("replace executable: %w (rollback failed: %v)", err, rollbackErr)
		}
		return fmt.Errorf("replace executable: %w", err)
	}
	if err := os.Remove(oldPath); err != nil {
		return nil
	}
	return nil
}

func retryExecutableSwap(ctx context.Context, realPath, newPath string) error {
	deadline := time.Now().Add(5 * time.Minute)
	for {
		if err := performExecutableSwap(realPath, newPath); err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting to replace executable")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func RunHelper(ctx context.Context, targetExe, incomingExe string) error {
	return retryExecutableSwap(ctx, targetExe, incomingExe)
}
