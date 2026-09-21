// Package validation provides reusable validation functions for flags and inputs.
//
// This follows the kubectl pattern of delegating to underlying libraries:
// - Chart names: Use Helm's own validators (ValidateMetadataName)
// - URLs: Standard Go url.Parse
// - Concurrency: System-aware bounds derived from CPU count
//
// Validators check format and constraints (NOT presence—Cobra handles that).
// All validators return an error if validation fails, following Go conventions.
// Flags marked with MarkFlagRequired() in cmd files are guaranteed to be non-empty.
//
// Example usage in cmd/pull.go PreRunE:
//
//	if err := validation.ValidateChartName("--chart", chartName); err != nil {
//		return err
//	}
package validation

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	containername "github.com/google/go-containerregistry/pkg/name"
)

var supportedDestinationPlatforms = []string{
	"darwin/amd64",
	"darwin/arm64",
	"linux/amd64",
	"linux/arm64",
	"windows/amd64",
	"windows/arm64",
}

// maxConcurrency derives sensible max concurrency from system specs.
// Use CPU count * 4 to allow some parallelism while respecting system resources.
func maxConcurrency() int {
	cpus := runtime.NumCPU()
	max := cpus * 4
	// Cap at 256 to prevent unreasonable values on high-core systems
	if max > 256 {
		max = 256
	}
	return max
}

// ValidateURL checks that a string is a valid repository URL.
// Delegates to Go's standard url.Parse.
func ValidateURL(name, value string, allowInsecureHTTP bool) error {
	u, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("%s must be a valid URL: %w", name, err)
	}
	// URL must have a scheme
	if u.Scheme == "" {
		return fmt.Errorf("%s must be a valid URL with scheme (http/https): %q", name, value)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%s must use https scheme (or oci://) for remote repos: %q", name, value)
	}
	if u.Scheme == "http" && !allowInsecureHTTP {
		return fmt.Errorf("%s must use https scheme unless --allow-insecure-http is set: %q", name, value)
	}
	return nil
}

// ValidateChartName checks that a string is a valid Helm chart name.
// Mirrors Helm's metadata-name validation without relying on the deprecated SDK helper.
func ValidateChartName(name, value string) error {
	if err := validateMetadataName(value); err != nil {
		return fmt.Errorf("%s %w", name, err)
	}
	return nil
}

// IsLocalChartPath reports whether value looks like a filesystem path rather
// than a chart name or remote reference. Existence is checked by Helm's chart
// loader; this only keeps path arguments out of chart-name validation.
func IsLocalChartPath(value string) bool {
	if value == "." || value == ".." {
		return true
	}
	if filepath.IsAbs(value) {
		return true
	}
	if schemeIndex := strings.Index(value, "://"); schemeIndex >= 0 {
		if firstSeparator := strings.IndexAny(value, `/\`); firstSeparator == schemeIndex+1 {
			return false
		}
	}
	return strings.ContainsAny(value, `/\`)
}

// IsOCIReference reports whether value uses the oci:// scheme.
func IsOCIReference(value string) bool {
	return strings.HasPrefix(strings.ToLower(value), "oci://")
}

var metadataNamePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`)

func validateMetadataName(name string) error {
	if name == "" || len(name) > 253 || !metadataNamePattern.MatchString(name) {
		return fmt.Errorf("invalid metadata name, must match regex %s and the length must not be longer than 253", metadataNamePattern.String())
	}
	return nil
}

// ValidateOCIRef checks that a string is a basic valid OCI chart reference.
// Supported forms:
//   - oci://registry.example.com/charts/mychart
//   - oci://localhost:5000/charts/mychart:1.2.3
func ValidateOCIRef(name, value string) error {
	if !IsOCIReference(value) {
		return fmt.Errorf("%s must start with oci://: %q", name, value)
	}

	ref := value[len("oci://"):]
	if strings.ContainsAny(ref, " \t\r\n") {
		return fmt.Errorf("%s must not contain whitespace: %q", name, value)
	}
	if ref == "" {
		return fmt.Errorf("%s must include registry host and chart path: %q", name, value)
	}

	parts := strings.Split(ref, "/")
	if len(parts) < 2 || parts[0] == "" {
		return fmt.Errorf("%s must include registry host and chart path: %q", name, value)
	}
	for _, part := range parts[1:] {
		if part == "" {
			return fmt.Errorf("%s must include non-empty chart path segments: %q", name, value)
		}
	}
	return nil
}

// ValidateImageRegistry checks that a string is a valid image registry.
// Basic validation: registry should be host:port or host, not a URL with scheme or path.
func ValidateImageRegistry(name, value string) error {
	// Check for empty
	if value == "" {
		return fmt.Errorf("%s must not be empty", name)
	}
	// Check for URL schemes like https:// (look for :// pattern)
	if idx := -1; len(value) >= 3 {
		for i := range value[:len(value)-2] {
			if value[i:i+3] == "://" {
				idx = i
				break
			}
		}
		if idx >= 0 {
			return fmt.Errorf("%s should not include protocol scheme: %q", name, value)
		}
	}
	// Check for paths (look for / after host:port)
	if idx := -1; len(value) > 0 {
		for i, c := range value {
			if c == '/' {
				idx = i
				break
			}
		}
		if idx > 0 {
			return fmt.Errorf("%s should not include path: %q", name, value)
		}
	}
	return nil
}

// SplitRegistryPath separates a host-only registry value from an optional
// namespace path. It trims trailing slashes and then splits on the first slash.
func SplitRegistryPath(value string) (host, path string) {
	clean := strings.TrimSuffix(value, "/")
	parts := strings.SplitN(clean, "/", 2)
	if len(parts) == 1 {
		return parts[0], ""
	}
	return parts[0], parts[1]
}

// ValidateImageRegistryWithPath validates a registry argument that can include
// an optional namespace path (host[/path]).
func ValidateImageRegistryWithPath(name, value string) error {
	if strings.Contains(value, "://") {
		return fmt.Errorf("%s should not include protocol scheme: %q", name, value)
	}

	host, path := SplitRegistryPath(value)
	if err := ValidateImageRegistry(name, host); err != nil {
		return err
	}
	if path == "" {
		return nil
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "" {
			return fmt.Errorf("%s has an invalid namespace path %q: contains empty path segment", name, path)
		}
	}

	if _, err := containername.NewRepository(host + "/" + path); err != nil {
		return fmt.Errorf("%s has an invalid namespace path %q: %w", name, path, err)
	}
	return nil
}

// ValidateConcurrency checks that concurrency is within reasonable bounds.
// Maximum is derived from system CPU count: CPUs * 4, capped at 256.
func ValidateConcurrency(name string, value int) error {
	if value < 1 {
		return fmt.Errorf("%s must be at least 1", name)
	}
	max := maxConcurrency()
	if value > max {
		return fmt.Errorf("%s must not exceed %d (derived from %d CPUs)", name, max, runtime.NumCPU())
	}
	return nil
}

// ValidateNonNegativeDuration checks that a duration is zero or greater.
// Zero is supported by callers that use it to disable optional work.
func ValidateNonNegativeDuration(name string, value time.Duration) error {
	if value < 0 {
		return fmt.Errorf("%s must not be negative", name)
	}
	return nil
}

// ValidateVersion checks that a version string is valid (basic validation).
// Empty version is OK (uses latest). Non-empty should not start with 'v'.
func ValidateVersion(name, value string) error {
	if value == "" {
		// Empty version is OK (uses latest)
		return nil
	}
	// Helm expects versions without v prefix (not enforced here, just a convention)
	// The actual version resolution happens at runtime when fetching the chart.
	return nil
}

// ParseDestinationPlatform validates os/arch format and returns normalized parts.
func ParseDestinationPlatform(value string) (string, string, error) {
	clean := strings.ToLower(strings.TrimSpace(value))
	parts := strings.Split(clean, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("must be in os/arch format, got %q", value)
	}
	return parts[0], parts[1], nil
}

func ValidateDestinationPlatform(name, value string) error {
	goos, goarch, err := ParseDestinationPlatform(value)
	if err != nil {
		return fmt.Errorf("%s %w", name, err)
	}
	platform := goos + "/" + goarch
	if !slices.Contains(supportedDestinationPlatforms, platform) {
		return fmt.Errorf("%s must be one of [%s], got %q", name, strings.Join(supportedDestinationPlatforms, ", "), value)
	}
	return nil
}

// ValidateImage checks that a string is a valid container image reference.
// Delegates to go-containerregistry's name.ParseReference.
func ValidateImage(name, value string) error {
	if value == "" {
		return fmt.Errorf("%s must not be empty", name)
	}
	if _, err := containername.ParseReference(value); err != nil {
		return fmt.Errorf("%s is not a valid image reference %q: %w", name, value, err)
	}
	return nil
}
