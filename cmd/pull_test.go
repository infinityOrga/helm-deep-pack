package cmd

import (
	"errors"
	"io"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"helm-deep-pack/internal/pull"
	"helm-deep-pack/internal/terminal"
	"helm-deep-pack/internal/termstyle"
)

func TestMain(m *testing.M) {
	code := m.Run()
	CleanupChartDirs()
	os.Exit(code)
}

func TestPullCmd_FlagsRegistered(t *testing.T) {
	flags := []string{"repo", "version", "output-dir", "concurrency", "values", "set", "destination-platform", "allow-insecure-http", "verbose"}
	for _, flag := range flags {
		AssertFlagExists(t, pullCmd, flag)
	}
}

func TestPullCmd_FlagShorthands(t *testing.T) {
	tests := map[string]string{
		"repo":                "r",
		"version":             "v",
		"output-dir":          "o",
		"concurrency":         "c",
		"values":              "f",
		"allow-insecure-http": "k",
		"verbose":             "V",
	}
	for flagName, shorthand := range tests {
		AssertFlagShorthand(t, pullCmd, flagName, shorthand)
	}
}

func TestPullCmd_FlagsNotExposed(t *testing.T) {
	AssertFlagNotExists(t, pullCmd, "registry")
}

func TestPullCmd_FlagTypes(t *testing.T) {
	tests := map[string]string{
		"repo":                 "string",
		"version":              "string",
		"output-dir":           "string",
		"concurrency":          "int",
		"values":               "stringArray",
		"set":                  "stringArray",
		"destination-platform": "string",
		"allow-insecure-http":  "bool",
		"verbose":              "bool",
	}
	for flagName, expectedType := range tests {
		AssertFlagType(t, pullCmd, flagName, expectedType)
	}
}

func TestPullCmd_FlagDefaults(t *testing.T) {
	tests := map[string]string{
		"repo":                 "",
		"version":              "",
		"output-dir":           "",
		"concurrency":          "4",
		"values":               "[]",
		"set":                  "[]",
		"destination-platform": "",
		"allow-insecure-http":  "false",
		"verbose":              "false",
	}
	for flagName, expectedDefault := range tests {
		AssertFlagDefault(t, pullCmd, flagName, expectedDefault)
	}
}

func TestPullCmd_ChartFlagRequired(t *testing.T) {
	output := ExecuteCommand(pullCmd, []string{})
	if output.Err == nil {
		t.Fatalf("expected error when chart argument is missing, got none")
	}
	errOutput := output.Stderr + output.Stdout
	if !strings.Contains(errOutput, "arg(s)") && !strings.Contains(errOutput, "received 0") {
		t.Fatalf("error message should mention missing args, got: %s", errOutput)
	}
}

func TestPullCmd_ValidateChartNameInvalid(t *testing.T) {
	invalidNames := []string{"Chart_Name", "CHART", "chart-", "chart@"}
	for _, name := range invalidNames {
		t.Run(name, func(t *testing.T) {
			output := ExecuteCommand(pullCmd, []string{name})
			if output.Err == nil {
				t.Fatalf("expected error for invalid chart name %q, got none", name)
			}
			if !strings.Contains(combinedErrorText(output), "chart") {
				t.Fatalf("expected chart-related error for %q, got: %s", name, combinedErrorText(output))
			}
		})
	}
}

func TestPullCmd_ValidateChartNameInvalidLeadingHyphenWithArgSeparator(t *testing.T) {
	output := ExecuteCommand(pullCmd, []string{"--", "-chart"})
	if output.Err == nil {
		t.Fatal("expected error for invalid chart name starting with hyphen, got none")
	}
	if !strings.Contains(combinedErrorText(output), "chart") {
		t.Fatalf("expected chart-related error, got: %s", combinedErrorText(output))
	}
}

func TestPullCmd_ValidateChartNameValid(t *testing.T) {
	validNames := []string{"nginx", "nginx-ingress", "prometheus-operator", "my-chart"}
	for _, name := range validNames {
		t.Run(name, func(t *testing.T) {
			capture, restore := spyPullRun(nil)
			defer restore()

			output := ExecuteCommand(pullCmd, []string{name})
			if output.Err != nil {
				t.Fatalf("expected valid chart name %q to pass validation, got: %v", name, output.Err)
			}
			if !capture.called {
				t.Fatalf("expected workflow to be called for chart %q", name)
			}
		})
	}
}

func TestPullCmd_ValidateRepoURLInvalid(t *testing.T) {
	invalidURLs := []string{"not-a-url", "ftp://example.com", "example.com", "ht!tp://example.com"}
	for _, url := range invalidURLs {
		t.Run(url, func(t *testing.T) {
			output := ExecuteCommand(pullCmd, []string{"nginx", "--repo", url})
			if output.Err == nil {
				t.Fatalf("expected error for invalid repo URL %q, got none", url)
			}
			text := combinedErrorText(output)
			if !strings.Contains(text, "repo") && !strings.Contains(text, "url") {
				t.Fatalf("expected repo/url attribution for %q, got: %s", url, text)
			}
		})
	}
}

func TestPullCmd_ValidateRepoURLValid(t *testing.T) {
	validURLs := []string{
		"https://charts.example.com",
		"https://kubernetes.github.io/ingress-nginx",
	}
	for _, url := range validURLs {
		t.Run(url, func(t *testing.T) {
			capture, restore := spyPullRun(nil)
			defer restore()

			output := ExecuteCommand(pullCmd, []string{"nginx", "--repo", url})
			if output.Err != nil {
				t.Fatalf("expected valid repo URL %q to pass validation, got: %v", url, output.Err)
			}
			if !capture.called {
				t.Fatalf("expected workflow to be called for repo %q", url)
			}
		})
	}
}

func TestPullCmd_ValidateRepoURLHTTPAllowedWithFlag(t *testing.T) {
	capture, restore := spyPullRun(nil)
	defer restore()

	output := ExecuteCommand(pullCmd, []string{"nginx", "--repo", "http://charts.example.com", "--allow-insecure-http"})
	if output.Err != nil {
		t.Fatalf("expected HTTP repo URL to pass when --allow-insecure-http is set, got: %v", output.Err)
	}
	if !capture.called {
		t.Fatal("expected workflow to be called for HTTP repo URL with --allow-insecure-http")
	}
}

func TestPullCmd_ValidateOCIChartRefValid(t *testing.T) {
	capture, restore := spyPullRun(nil)
	defer restore()

	output := ExecuteCommand(pullCmd, []string{"oci://localhost:5000/charts/prometheus-node-exporter", "--version", "4.55.0"})
	if output.Err != nil {
		t.Fatalf("expected valid OCI chart ref to pass validation, got: %v", output.Err)
	}
	if !capture.called {
		t.Fatal("expected workflow to be called for OCI chart ref")
	}
	if capture.opts.Chart != "oci://localhost:5000/charts/prometheus-node-exporter" {
		t.Fatalf("expected OCI chart option to pass through, got %q", capture.opts.Chart)
	}
}

func TestPullCmd_ValidateOCIRepoRefValid(t *testing.T) {
	capture, restore := spyPullRun(nil)
	defer restore()

	output := ExecuteCommand(pullCmd, []string{"prometheus-node-exporter", "--repo", "oci://localhost:5000/charts", "--version", "4.55.0"})
	if output.Err != nil {
		t.Fatalf("expected valid OCI repo ref to pass validation, got: %v", output.Err)
	}
	if !capture.called {
		t.Fatal("expected workflow to be called for OCI repo ref")
	}
	if capture.opts.Repo != "oci://localhost:5000/charts" {
		t.Fatalf("expected OCI repo option to pass through, got %q", capture.opts.Repo)
	}
}

func TestPullCmd_ValidateOCIChartRefWithRepoRejected(t *testing.T) {
	output := ExecuteCommand(pullCmd, []string{
		"oci://localhost:5000/charts/prometheus-node-exporter",
		"--repo", "oci://localhost:5000/charts",
	})
	if output.Err == nil {
		t.Fatal("expected mixed OCI chart arg + --repo to be rejected")
	}
	if !strings.Contains(combinedErrorText(output), "--repo cannot be combined") {
		t.Fatalf("expected mixed OCI error, got: %s", combinedErrorText(output))
	}
}

func TestPullCmd_ValidateConcurrencyInvalid(t *testing.T) {
	tests := map[string]string{"zero": "0", "negative": "-1", "toolarge": "10000"}
	for testName, concurrency := range tests {
		t.Run(testName, func(t *testing.T) {
			output := ExecuteCommand(pullCmd, []string{"nginx", "--concurrency", concurrency})
			if output.Err == nil {
				t.Fatalf("expected error for concurrency=%s, got none", concurrency)
			}
			if !strings.Contains(combinedErrorText(output), "concurrency") {
				t.Fatalf("expected concurrency-related error, got: %s", combinedErrorText(output))
			}
		})
	}
}

func TestPullCmd_ValidateConcurrencyValid(t *testing.T) {
	validConcurrencies := []string{"1", "4", "8", "16"}
	for _, concurrency := range validConcurrencies {
		t.Run(concurrency, func(t *testing.T) {
			capture, restore := spyPullRun(nil)
			defer restore()

			output := ExecuteCommand(pullCmd, []string{"nginx", "--concurrency", concurrency})
			if output.Err != nil {
				t.Fatalf("expected valid concurrency %s to pass validation, got: %v", concurrency, output.Err)
			}
			if !capture.called {
				t.Fatalf("expected workflow to be called for concurrency=%s", concurrency)
			}
		})
	}
}

func TestPullCmd_ValidateDestinationPlatform(t *testing.T) {
	capture, restore := spyPullRun(nil)
	defer restore()

	output := ExecuteCommand(pullCmd, []string{"nginx", "--destination-platform", "WINDOWS/AMD64"})
	if output.Err != nil {
		t.Fatalf("expected destination platform to validate, got: %v", output.Err)
	}
	if !capture.called {
		t.Fatal("expected workflow to be called")
	}
	if capture.opts.DestinationPlatform != "windows/amd64" {
		t.Fatalf("expected normalized destination platform, got %q", capture.opts.DestinationPlatform)
	}
}

func TestPullCmd_ValidateDestinationPlatformInvalid(t *testing.T) {
	output := ExecuteCommand(pullCmd, []string{"nginx", "--destination-platform", "freebsd/amd64"})
	if output.Err == nil {
		t.Fatal("expected error for unsupported destination platform")
	}
	if !strings.Contains(combinedErrorText(output), "destination-platform") {
		t.Fatalf("expected destination-platform attribution, got: %s", combinedErrorText(output))
	}
}

func TestPullCmd_FlagsMapToOptions(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want pull.Options
	}{
		{
			name: "minimal",
			args: []string{"nginx"},
			want: pull.Options{Chart: "nginx", Concurrency: 4},
		},
		{
			name: "with repo",
			args: []string{"nginx", "--repo", "https://charts.example.com"},
			want: pull.Options{Chart: "nginx", Repo: "https://charts.example.com", Concurrency: 4},
		},
		{
			name: "with repo shorthand",
			args: []string{"nginx", "-r", "https://charts.example.com"},
			want: pull.Options{Chart: "nginx", Repo: "https://charts.example.com", Concurrency: 4},
		},
		{
			name: "with version",
			args: []string{"nginx", "--version", "1.0.0"},
			want: pull.Options{Chart: "nginx", Version: "1.0.0", Concurrency: 4},
		},
		{
			name: "with version shorthand",
			args: []string{"nginx", "-v", "1.0.0"},
			want: pull.Options{Chart: "nginx", Version: "1.0.0", Concurrency: 4},
		},
		{
			name: "with output-dir",
			args: []string{"nginx", "--output-dir", "/tmp/output"},
			want: pull.Options{Chart: "nginx", OutputDir: "/tmp/output", Concurrency: 4},
		},
		{
			name: "with output-dir shorthand",
			args: []string{"nginx", "-o", "/tmp/output"},
			want: pull.Options{Chart: "nginx", OutputDir: "/tmp/output", Concurrency: 4},
		},
		{
			name: "with concurrency",
			args: []string{"nginx", "--concurrency", "8"},
			want: pull.Options{Chart: "nginx", Concurrency: 8},
		},
		{
			name: "with concurrency shorthand",
			args: []string{"nginx", "-c", "8"},
			want: pull.Options{Chart: "nginx", Concurrency: 8},
		},
		{
			name: "with destination platform",
			args: []string{"nginx", "--destination-platform", "windows/amd64"},
			want: pull.Options{Chart: "nginx", Concurrency: 4, DestinationPlatform: "windows/amd64"},
		},
		{
			name: "with one values file",
			args: []string{"nginx", "--values", "values.yaml"},
			want: pull.Options{Chart: "nginx", Concurrency: 4, ValuesFiles: []string{"values.yaml"}},
		},
		{
			name: "with ordered values files",
			args: []string{"nginx", "-f", "values-a.yaml", "-f", "values-b.yaml"},
			want: pull.Options{Chart: "nginx", Concurrency: 4, ValuesFiles: []string{"values-a.yaml", "values-b.yaml"}},
		},
		{
			name: "with set overrides",
			args: []string{"nginx", "--set", "image.tag=v2", "--set", "sidecar.enabled=true"},
			want: pull.Options{Chart: "nginx", Concurrency: 4, SetValues: []string{"image.tag=v2", "sidecar.enabled=true"}},
		},
		{
			name: "with values and set",
			args: []string{"nginx", "-f", "values.yaml", "--set", "image.tag=v3"},
			want: pull.Options{
				Chart:       "nginx",
				Concurrency: 4,
				ValuesFiles: []string{"values.yaml"},
				SetValues:   []string{"image.tag=v3"},
			},
		},
		{
			name: "all flags",
			args: []string{
				"nginx",
				"-r", "https://charts.bitnami.com/bitnami",
				"-v", "14.0.0",
				"-o", "/tmp/nginx-mirror",
				"-c", "8",
				"-f", "values.yaml",
				"--set", "image.tag=v2",
				"-V",
			},
			want: pull.Options{
				Chart:       "nginx",
				Repo:        "https://charts.bitnami.com/bitnami",
				Version:     "14.0.0",
				OutputDir:   "/tmp/nginx-mirror",
				Concurrency: 8,
				ValuesFiles: []string{"values.yaml"},
				SetValues:   []string{"image.tag=v2"},
			},
		},
		{
			name: "oci chart arg",
			args: []string{
				"oci://localhost:5000/charts/nginx",
				"--version", "14.0.0",
			},
			want: pull.Options{
				Chart:       "oci://localhost:5000/charts/nginx",
				Version:     "14.0.0",
				Concurrency: 4,
			},
		},
		{
			name: "oci repo split",
			args: []string{
				"nginx",
				"-r", "oci://localhost:5000/charts",
				"-v", "14.0.0",
			},
			want: pull.Options{
				Chart:       "nginx",
				Repo:        "oci://localhost:5000/charts",
				Version:     "14.0.0",
				Concurrency: 4,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			capture, restore := spyPullRun(nil)
			defer restore()

			output := ExecuteCommand(pullCmd, tt.args)
			if output.Err != nil {
				t.Fatalf("unexpected error: %v\nstderr: %s", output.Err, output.Stderr)
			}
			if !capture.called {
				t.Fatal("expected workflow to be invoked, it was not")
			}
			tt.want.HelperVersion = Version()
			if !reflect.DeepEqual(capture.opts, tt.want) {
				t.Fatalf("Options mismatch:\n got  %+v\n want %+v", capture.opts, tt.want)
			}
		})
	}
}

func TestPullCmd_WarnsForNonRecommendedDestination(t *testing.T) {
	capture, restore := spyPullRun(nil)
	defer restore()

	output := ExecuteCommand(pullCmd, []string{"nginx", "--destination-platform", "linux/amd64"})
	if output.Err != nil {
		t.Fatalf("unexpected error: %v", output.Err)
	}
	if !capture.called {
		t.Fatal("expected workflow to be called")
	}
	if !strings.Contains(strings.ToLower(output.Stderr), "warning: destination platform is") {
		t.Fatalf("expected warning in stderr, got: %q", output.Stderr)
	}
	if !strings.Contains(output.Stderr, "--destination-platform windows/amd64") {
		t.Fatalf("expected fix hint in warning, got: %q", output.Stderr)
	}
}

func TestPullCmd_NoWarningForRecommendedDestination(t *testing.T) {
	_, restore := spyPullRun(nil)
	defer restore()

	output := ExecuteCommand(pullCmd, []string{"nginx", "--destination-platform", "windows/amd64"})
	if output.Err != nil {
		t.Fatalf("unexpected error: %v", output.Err)
	}
	if strings.Contains(strings.ToLower(output.Stderr), "warning: destination platform is") {
		t.Fatalf("did not expect warning for windows/amd64, got: %q", output.Stderr)
	}
}

func TestPullCmd_DefaultDestinationWarningDependsOnHostPlatform(t *testing.T) {
	_, restore := spyPullRun(nil)
	defer restore()

	output := ExecuteCommand(pullCmd, []string{"nginx"})
	if output.Err != nil {
		t.Fatalf("unexpected error: %v", output.Err)
	}
	hasWarning := strings.Contains(strings.ToLower(output.Stderr), "warning: destination platform is")
	if runtime.GOOS == "windows" && runtime.GOARCH == "amd64" {
		if hasWarning {
			t.Fatalf("did not expect warning on windows/amd64 host, got: %q", output.Stderr)
		}
		return
	}
	if !hasWarning {
		t.Fatalf("expected warning on non-windows/amd64 host, got: %q", output.Stderr)
	}
}

func TestPullCmd_WarningColoredForTerminal(t *testing.T) {
	orig := terminal.IsWriter
	terminal.IsWriter = func(w io.Writer) bool {
		return true
	}
	defer func() { terminal.IsWriter = orig }()

	_, restore := spyPullRun(nil)
	defer restore()
	output := ExecuteCommand(pullCmd, []string{"nginx", "--destination-platform", "linux/amd64"})
	if output.Err != nil {
		t.Fatalf("unexpected error: %v", output.Err)
	}
	if !strings.Contains(output.Stderr, termstyle.Yellow+"warning:") || !strings.Contains(output.Stderr, termstyle.Reset) {
		t.Fatalf("expected yellow warning output, got: %q", output.Stderr)
	}
}

func TestPullCmd_WorkflowErrorPropagates(t *testing.T) {
	_, restore := spyPullRun(errors.New("boom"))
	defer restore()

	output := ExecuteCommand(pullCmd, []string{"nginx"})
	if output.Err == nil {
		t.Fatalf("expected workflow error to propagate, got nil")
	}
	if !strings.Contains(output.Err.Error(), "boom") {
		t.Fatalf("expected workflow error to contain boom, got: %v", output.Err)
	}
}

func TestPullCmd_HelpFlag(t *testing.T) {
	output := ExecuteCommand(pullCmd, []string{"--help"})
	if output.Err != nil {
		t.Fatalf("--help should not produce error: %v", output.Err)
	}
	helpOutput := output.Stdout + output.Stderr
	if !strings.Contains(helpOutput, "Render") || !strings.Contains(helpOutput, "chart") {
		t.Fatalf("help output missing expected content: %s", helpOutput)
	}
}

func TestPullCmd_UnknownFlag(t *testing.T) {
	output := ExecuteCommand(pullCmd, []string{"nginx", "--unknown-flag", "value"})
	if output.Err == nil {
		t.Fatalf("expected error for unknown flag, got none")
	}
}
