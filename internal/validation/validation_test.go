package validation

import (
	"strings"
	"testing"
	"time"
)

func TestValidateURL(t *testing.T) {
	tests := []struct {
		name            string
		value           string
		allowInsecure   bool
		wantErr         bool
		wantErrContains string
	}{
		{"valid http URL with opt-in", "http://example.com", true, false, ""},
		{"plain http URL rejected by default", "http://example.com", false, true, "--allow-insecure-http"},
		{"valid https URL", "https://example.com/path", false, false, ""},
		{"empty", "", false, true, "valid URL"},
		{"no scheme", "example.com", false, true, "scheme"},
		{"invalid URL", "ht!tp://example.com", false, true, "valid URL"},
		{"unsupported scheme", "ftp://example.com", false, true, "https scheme"},
		{"oci scheme", "oci://registry.example.com/charts", false, true, "https scheme"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateURL("test", tt.value, tt.allowInsecure)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateURL() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErrContains != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErrContains)) {
				t.Errorf("ValidateURL() error = %v, want to contain %q", err, tt.wantErrContains)
			}
		})
	}
}

func TestValidateOCIRef(t *testing.T) {
	tests := []struct {
		name            string
		value           string
		wantErr         bool
		wantErrContains string
	}{
		{"valid chart ref", "oci://registry.example.com/charts/my-chart", false, ""},
		{"valid localhost ref", "oci://localhost:5000/charts/my-chart:1.2.3", false, ""},
		{"valid uppercase scheme", "OCI://localhost:5000/charts/my-chart", false, ""},
		{"missing scheme", "registry.example.com/charts/my-chart", true, "must start with oci://"},
		{"missing host", "oci:///charts/my-chart", true, "host and chart path"},
		{"missing path", "oci://registry.example.com", true, "host and chart path"},
		{"empty path segment", "oci://registry.example.com/charts//my-chart", true, "non-empty chart path"},
		{"whitespace", "oci://registry.example.com/charts/my chart", true, "whitespace"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateOCIRef("test", tt.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateOCIRef() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErrContains != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErrContains)) {
				t.Errorf("ValidateOCIRef() error = %v, want to contain %q", err, tt.wantErrContains)
			}
		})
	}
}

func TestIsOCIReference(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{"oci lowercase", "oci://registry.example.com/charts/my-chart", true},
		{"oci uppercase", "OCI://registry.example.com/charts/my-chart", true},
		{"https url", "https://registry.example.com/charts", false},
		{"plain chart", "my-chart", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsOCIReference(tt.value)
			if got != tt.want {
				t.Fatalf("IsOCIReference(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

func TestValidateChartName(t *testing.T) {
	tests := []struct {
		name            string
		value           string
		wantErr         bool
		wantErrContains string
	}{
		{"valid name", "my-chart", false, ""},
		{"alphanumeric", "chart123", false, ""},
		{"single char", "a", false, ""},
		{"empty", "", true, "invalid metadata name"},
		{"uppercase", "MyChart", true, "invalid metadata name"},
		{"space", "my chart", true, "invalid metadata name"},
		{"starts with hyphen", "-mychart", true, "invalid metadata name"},
		{"ends with hyphen", "mychart-", true, "invalid metadata name"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateChartName("test", tt.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateChartName() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErrContains != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErrContains)) {
				t.Errorf("ValidateChartName() error = %v, want to contain %q", err, tt.wantErrContains)
			}
		})
	}
}

func TestIsLocalChartPath(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{"relative directory", "./charts/my-chart", true},
		{"parent directory", "../charts/my-chart", true},
		{"nested relative path", "charts/my-chart", true},
		{"absolute path", "/tmp/charts/my-chart", true},
		{"windows path", `C:\charts\my-chart`, true},
		{"current directory", ".", true},
		{"parent directory shorthand", "..", true},
		{"absolute path with scheme-like filename", "/tmp/chart://variant", true},
		{"relative path with scheme-like filename", "./charts/chart://variant", true},
		{"chart name", "my-chart", false},
		{"OCI reference", "oci://registry.example.com/charts/my-chart", false},
		{"HTTP URL", "https://charts.example.com/my-chart", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsLocalChartPath(tt.value); got != tt.want {
				t.Fatalf("IsLocalChartPath(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

func TestValidateConcurrency(t *testing.T) {
	tests := []struct {
		name            string
		value           int
		wantErr         bool
		wantErrContains string
	}{
		{"valid concurrency", 1, false, ""},
		{"valid concurrency 4", 4, false, ""},
		{"zero concurrency", 0, true, "must be at least 1"},
		{"negative concurrency", -1, true, "must be at least 1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateConcurrency("test", tt.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateConcurrency() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErrContains != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErrContains)) {
				t.Errorf("ValidateConcurrency() error = %v, want to contain %q", err, tt.wantErrContains)
			}
		})
	}

}

func TestValidateNonNegativeDuration(t *testing.T) {
	tests := []struct {
		name      string
		value     time.Duration
		wantError bool
	}{
		{name: "zero", value: 0},
		{name: "positive", value: time.Second},
		{name: "negative", value: -time.Second, wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateNonNegativeDuration("test", tt.value)
			if (err != nil) != tt.wantError {
				t.Fatalf("ValidateNonNegativeDuration() error = %v, wantError %v", err, tt.wantError)
			}
			if tt.wantError && !strings.Contains(err.Error(), "must not be negative") {
				t.Fatalf("ValidateNonNegativeDuration() error = %v, want negative-duration message", err)
			}
		})
	}
}

func TestValidateImageRegistry(t *testing.T) {
	tests := []struct {
		name            string
		value           string
		wantErr         bool
		wantErrContains string
	}{
		{"valid registry", "localhost:5000", false, ""},
		{"registry without port", "registry.example.com", false, ""},
		{"registry with subdomain", "gcr.io", false, ""},
		{"local registry", "localhost", false, ""},
		{"empty", "", true, "must not be empty"},
		{"with protocol", "https://registry.example.com", true, "should not include protocol scheme"},
		{"with path", "registry.example.com/path", true, "should not include path"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateImageRegistry("test", tt.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateImageRegistry() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErrContains != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErrContains)) {
				t.Errorf("ValidateImageRegistry() error = %v, want to contain %q", err, tt.wantErrContains)
			}
		})
	}
}

func TestValidateImageRegistryWithPath(t *testing.T) {
	tests := []struct {
		name            string
		value           string
		wantErr         bool
		wantErrContains string
	}{
		{"valid registry", "registry.example.com", false, ""},
		{"valid registry with path", "registry.example.com/team/sub", false, ""},
		{"valid registry with trailing slash", "registry.example.com/team/sub/", false, ""},
		{"empty", "", true, "must not be empty"},
		{"rejects scheme", "https://registry.example.com/team", true, "should not include protocol scheme"},
		{"rejects uppercase path", "registry.example.com/Team", true, "invalid namespace path"},
		{"rejects empty path segment", "registry.example.com/team//sub", true, "invalid namespace path"},
		{"rejects trailing empty path segment", "registry.example.com/team//", true, "invalid namespace path"},
		{"rejects path with tag", "registry.example.com/team:1.0", true, "invalid namespace path"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateImageRegistryWithPath("test", tt.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateImageRegistryWithPath() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErrContains != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErrContains)) {
				t.Errorf("ValidateImageRegistryWithPath() error = %v, want to contain %q", err, tt.wantErrContains)
			}
		})
	}
}

func TestSplitRegistryPath(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantHost string
		wantPath string
	}{
		{"host only", "registry.example.com", "registry.example.com", ""},
		{"host and path", "registry.example.com/team/sub", "registry.example.com", "team/sub"},
		{"host and path trailing slash", "registry.example.com/team/sub/", "registry.example.com", "team/sub"},
		{"host with trailing slash only", "registry.example.com/", "registry.example.com", ""},
		{"leading slash", "/team/sub", "", "team/sub"},
		{"empty input", "", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, path := SplitRegistryPath(tt.input)
			if host != tt.wantHost || path != tt.wantPath {
				t.Fatalf("SplitRegistryPath(%q) = host=%q path=%q, want host=%q path=%q", tt.input, host, path, tt.wantHost, tt.wantPath)
			}
		})
	}
}

func TestValidateImage(t *testing.T) {
	tests := []struct {
		name            string
		value           string
		wantErr         bool
		wantErrContains string
	}{
		{"valid tag", "nginx:1.27", false, ""},
		{"valid digest", "docker.io/library/redis@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", false, ""},
		{"empty", "", true, "must not be empty"},
		{"uppercase rejected", "UPPERCASE:TAG", true, "not a valid image reference"},
		{"bad digest format", "image@digest", true, "not a valid image reference"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateImage("test", tt.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateImage() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErrContains != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErrContains)) {
				t.Errorf("ValidateImage() error = %v, want to contain %q", err, tt.wantErrContains)
			}
		})
	}
}

func TestParseDestinationPlatform(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantOS    string
		wantArch  string
		wantError bool
	}{
		{name: "valid", input: "windows/amd64", wantOS: "windows", wantArch: "amd64"},
		{name: "valid uppercase and spaced", input: "  WINDOWS/ARM64  ", wantOS: "windows", wantArch: "arm64"},
		{name: "missing arch", input: "windows", wantError: true},
		{name: "too many segments", input: "windows/amd64/extra", wantError: true},
		{name: "empty", input: "", wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotOS, gotArch, err := ParseDestinationPlatform(tt.input)
			if (err != nil) != tt.wantError {
				t.Fatalf("ParseDestinationPlatform() err = %v, wantError %v", err, tt.wantError)
			}
			if tt.wantError {
				return
			}
			if gotOS != tt.wantOS || gotArch != tt.wantArch {
				t.Fatalf("ParseDestinationPlatform() = %s/%s, want %s/%s", gotOS, gotArch, tt.wantOS, tt.wantArch)
			}
		})
	}
}

func TestValidateDestinationPlatform(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantError bool
	}{
		{name: "supported", input: "linux/amd64"},
		{name: "supported uppercase", input: "WINDOWS/AMD64"},
		{name: "unsupported combo", input: "freebsd/amd64", wantError: true},
		{name: "invalid format", input: "linux-amd64", wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDestinationPlatform("--destination-platform", tt.input)
			if (err != nil) != tt.wantError {
				t.Fatalf("ValidateDestinationPlatform() err = %v, wantError %v", err, tt.wantError)
			}
		})
	}
}
