package cmd

import (
	"errors"
	"strings"
	"testing"
)

func TestPushCmd_FlagsRegistered(t *testing.T) {
	flags := []string{"input-dir", "concurrency", "all", "allow-insecure-http", "verbose"}
	for _, flag := range flags {
		AssertFlagExists(t, pushCmd, flag)
	}
}

func TestPushCmd_FlagShorthands(t *testing.T) {
	tests := map[string]string{
		"input-dir":           "i",
		"concurrency":         "c",
		"all":                 "a",
		"allow-insecure-http": "k",
		"verbose":             "V",
	}
	for flagName, shorthand := range tests {
		AssertFlagShorthand(t, pushCmd, flagName, shorthand)
	}
}

func TestPushCmd_FlagsNotExposed(t *testing.T) {
	flags := []string{"registry", "chart", "repo", "version", "output-dir"}
	for _, flag := range flags {
		AssertFlagNotExists(t, pushCmd, flag)
	}
}

func TestPushCmd_FlagTypes(t *testing.T) {
	tests := map[string]string{
		"input-dir":           "string",
		"concurrency":         "int",
		"all":                 "bool",
		"allow-insecure-http": "bool",
		"verbose":             "bool",
	}
	for flagName, expectedType := range tests {
		AssertFlagType(t, pushCmd, flagName, expectedType)
	}
}

func TestPushCmd_FlagDefaults(t *testing.T) {
	tests := map[string]string{
		"input-dir":           "",
		"concurrency":         "4",
		"all":                 "false",
		"allow-insecure-http": "false",
		"verbose":             "false",
	}
	for flagName, expectedDefault := range tests {
		AssertFlagDefault(t, pushCmd, flagName, expectedDefault)
	}
}

func TestPushCmd_RegistryArgOptional(t *testing.T) {
	capture, restore := spyPushRun(nil)
	defer restore()

	output := ExecuteCommand(pushCmd, []string{})
	if output.Err != nil {
		t.Fatalf("expected zero-arg push to be accepted, got: %v", output.Err)
	}
	if !capture.called {
		t.Fatal("expected workflow to be invoked when registry argument is omitted")
	}
	if capture.opts.Registry != "" {
		t.Fatalf("expected empty registry passed to workflow, got %q", capture.opts.Registry)
	}
}

func TestPushCmd_RegistryArgMissingNonInteractive(t *testing.T) {
	output := ExecuteCommand(pushCmd, []string{})
	if output.Err == nil {
		t.Fatalf("expected error when registry is missing in a non-interactive context, got none")
	}
	if !strings.Contains(combinedErrorText(output), "registry") {
		t.Fatalf("error should be attributable to the missing registry, got: %s", combinedErrorText(output))
	}
}

func TestPushCmd_ValidateRegistryInvalid(t *testing.T) {
	invalidRegistries := []string{
		"https://example.com",
		"http://registry.example.com",
		"registry.example.com/Team",
		"registry.example.com/team//sub",
	}
	for _, registry := range invalidRegistries {
		t.Run(registry, func(t *testing.T) {
			output := ExecuteCommand(pushCmd, []string{registry})
			if output.Err == nil {
				t.Fatalf("expected error for invalid registry %q, got none", registry)
			}
			text := combinedErrorText(output)
			if !strings.Contains(text, "registry") && !strings.Contains(text, "scheme") && !strings.Contains(text, "path") {
				t.Fatalf("expected registry-related attribution, got: %s", text)
			}
		})
	}
}

func TestPushCmd_ValidateRegistryValid(t *testing.T) {
	validRegistries := []string{
		"docker.io",
		"registry.example.com",
		"registry.example.com:5000",
		"localhost:5000",
		"quay.io",
		"registry.example.com/team",
		"localhost:5000/team/sub",
	}
	for _, registry := range validRegistries {
		t.Run(registry, func(t *testing.T) {
			capture, restore := spyPushRun(nil)
			defer restore()

			output := ExecuteCommand(pushCmd, []string{registry})
			if output.Err != nil {
				t.Fatalf("expected valid registry %q to pass validation, got: %v", registry, output.Err)
			}
			if !capture.called {
				t.Fatalf("expected workflow to be called for registry %q", registry)
			}
		})
	}
}

func TestPushCmd_ValidateConcurrencyInvalid(t *testing.T) {
	tests := map[string]string{"zero": "0", "negative": "-1", "toolarge": "10000"}
	for testName, concurrency := range tests {
		t.Run(testName, func(t *testing.T) {
			output := ExecuteCommand(pushCmd, []string{"docker.io", "--concurrency", concurrency})
			if output.Err == nil {
				t.Fatalf("expected error for concurrency=%s, got none", concurrency)
			}
			if !strings.Contains(combinedErrorText(output), "concurrency") {
				t.Fatalf("expected concurrency-related error, got: %s", combinedErrorText(output))
			}
		})
	}
}

func TestPushCmd_FlagsMapToArgs(t *testing.T) {
	tests := []struct {
		name            string
		args            []string
		wantRegistry    string
		wantInputDir    string
		wantConcurrency int
		wantAll         bool
		wantAllowHTTP   bool
	}{
		{"minimal", []string{"docker.io"}, "docker.io", "", 4, false, false},
		{"with input-dir", []string{"docker.io", "--input-dir", "/tmp/images"}, "docker.io", "/tmp/images", 4, false, false},
		{"with input-dir shorthand", []string{"docker.io", "-i", "/tmp/images"}, "docker.io", "/tmp/images", 4, false, false},
		{"with concurrency", []string{"docker.io", "--concurrency", "8"}, "docker.io", "", 8, false, false},
		{"with concurrency shorthand", []string{"docker.io", "-c", "8"}, "docker.io", "", 8, false, false},
		{"with allow insecure http", []string{"docker.io", "--allow-insecure-http"}, "docker.io", "", 4, false, true},
		{"with allow insecure http shorthand", []string{"docker.io", "-k"}, "docker.io", "", 4, false, true},
		{"with namespace path", []string{"registry.example.com:5000/team/sub"}, "registry.example.com:5000/team/sub", "", 4, false, false},
		{
			"all flags",
			[]string{"registry.example.com:5000", "-i", "/tmp/nginx-mirror", "-c", "8", "-a", "-k", "-V"},
			"registry.example.com:5000",
			"/tmp/nginx-mirror",
			8,
			true,
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			capture, restore := spyPushRun(nil)
			defer restore()

			output := ExecuteCommand(pushCmd, tt.args)
			if output.Err != nil {
				t.Fatalf("unexpected error: %v\nstderr: %s", output.Err, output.Stderr)
			}
			if !capture.called {
				t.Fatal("expected workflow to be invoked")
			}
			if capture.opts.Registry != tt.wantRegistry || capture.opts.InputDir != tt.wantInputDir || capture.opts.Concurrency != tt.wantConcurrency || capture.opts.All != tt.wantAll || capture.opts.AllowInsecureHTTP != tt.wantAllowHTTP {
				t.Fatalf(
					"args mismatch:\n got reg=%q dir=%q conc=%d all=%v allowHTTP=%v\nwant reg=%q dir=%q conc=%d all=%v allowHTTP=%v",
					capture.opts.Registry, capture.opts.InputDir, capture.opts.Concurrency, capture.opts.All, capture.opts.AllowInsecureHTTP,
					tt.wantRegistry, tt.wantInputDir, tt.wantConcurrency, tt.wantAll, tt.wantAllowHTTP,
				)
			}
		})
	}
}

func TestPushCmd_WorkflowErrorPropagates(t *testing.T) {
	_, restore := spyPushRun(errors.New("boom"))
	defer restore()

	output := ExecuteCommand(pushCmd, []string{"docker.io"})
	if output.Err == nil {
		t.Fatalf("expected workflow error to propagate, got nil")
	}
	if !strings.Contains(output.Err.Error(), "boom") {
		t.Fatalf("expected workflow error to contain boom, got: %v", output.Err)
	}
}

func TestPushCmd_HelpFlag(t *testing.T) {
	output := ExecuteCommand(pushCmd, []string{"--help"})
	if output.Err != nil {
		t.Fatalf("--help should not produce error: %v", output.Err)
	}
	helpOutput := output.Stdout + output.Stderr
	if !strings.Contains(helpOutput, "Push") || !strings.Contains(helpOutput, "images") {
		t.Fatalf("help output missing expected content: %s", helpOutput)
	}
}

func TestPushCmd_UnknownFlag(t *testing.T) {
	output := ExecuteCommand(pushCmd, []string{"docker.io", "--unknown-flag", "value"})
	if output.Err == nil {
		t.Fatalf("expected error for unknown flag, got none")
	}
}
