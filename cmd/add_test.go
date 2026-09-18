package cmd

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"helm-deep-pack/internal/add"
)

func TestAddCmd_FlagsRegistered(t *testing.T) {
	flags := []string{"output-dir", "concurrency", "verbose"}
	for _, flag := range flags {
		AssertFlagExists(t, addCmd, flag)
	}
}

func TestAddCmd_FlagShorthands(t *testing.T) {
	tests := map[string]string{
		"output-dir":  "o",
		"concurrency": "c",
		"verbose":     "V",
	}
	for flagName, shorthand := range tests {
		AssertFlagShorthand(t, addCmd, flagName, shorthand)
	}
}

func TestAddCmd_FlagsNotExposed(t *testing.T) {
	AssertFlagNotExists(t, addCmd, "repo")
	AssertFlagNotExists(t, addCmd, "version")
	AssertFlagNotExists(t, addCmd, "registry")
}

func TestAddCmd_FlagTypes(t *testing.T) {
	tests := map[string]string{
		"output-dir":  "string",
		"concurrency": "int",
		"verbose":     "bool",
	}
	for flagName, expectedType := range tests {
		AssertFlagType(t, addCmd, flagName, expectedType)
	}
}

func TestAddCmd_FlagDefaults(t *testing.T) {
	tests := map[string]string{
		"output-dir":  "",
		"concurrency": "4",
		"verbose":     "false",
	}
	for flagName, expectedDefault := range tests {
		AssertFlagDefault(t, addCmd, flagName, expectedDefault)
	}
}

func TestAddCmd_ImageArgumentRequired(t *testing.T) {
	output := ExecuteCommand(addCmd, []string{})
	if output.Err == nil {
		t.Fatalf("expected error when image argument is missing, got none")
	}
	errOutput := output.Stderr + output.Stdout
	if !strings.Contains(errOutput, "arg(s)") && !strings.Contains(errOutput, "received 0") {
		t.Fatalf("error message should mention missing args, got: %s", errOutput)
	}
}

func TestAddCmd_ValidateImageInvalid(t *testing.T) {
	invalidImages := []string{"not a valid image", "image:tag:extra", ""}
	for _, img := range invalidImages {
		t.Run(img, func(t *testing.T) {
			output := ExecuteCommand(addCmd, []string{img})
			if output.Err == nil {
				t.Fatalf("expected error for invalid image %q, got none", img)
			}
			if !strings.Contains(combinedErrorText(output), "image") {
				t.Fatalf("expected image-related error for %q, got: %s", img, combinedErrorText(output))
			}
		})
	}
}

func TestAddCmd_ValidateImageValid(t *testing.T) {
	validImages := []string{"nginx", "nginx:latest", "docker.io/library/nginx:1.21", "gcr.io/project/image:tag"}
	for _, img := range validImages {
		t.Run(img, func(t *testing.T) {
			capture, restore := spyAddRun(nil)
			defer restore()

			output := ExecuteCommand(addCmd, []string{img})
			if output.Err != nil {
				t.Fatalf("expected valid image %q to pass validation, got: %v", img, output.Err)
			}
			if !capture.called {
				t.Fatalf("expected workflow to be called for image %q", img)
			}
		})
	}
}

func TestAddCmd_ValidateConcurrencyInvalid(t *testing.T) {
	tests := map[string]string{"zero": "0", "negative": "-1", "toolarge": "10000"}
	for testName, concurrency := range tests {
		t.Run(testName, func(t *testing.T) {
			output := ExecuteCommand(addCmd, []string{"nginx", "--concurrency", concurrency})
			if output.Err == nil {
				t.Fatalf("expected error for concurrency=%s, got none", concurrency)
			}
			if !strings.Contains(combinedErrorText(output), "concurrency") {
				t.Fatalf("expected concurrency-related error, got: %s", combinedErrorText(output))
			}
		})
	}
}

func TestAddCmd_ValidateConcurrencyValid(t *testing.T) {
	validConcurrencies := validConcurrencyValues()
	for _, concurrency := range validConcurrencies {
		t.Run(concurrency, func(t *testing.T) {
			capture, restore := spyAddRun(nil)
			defer restore()

			output := ExecuteCommand(addCmd, []string{"nginx", "--concurrency", concurrency})
			if output.Err != nil {
				t.Fatalf("expected valid concurrency %s to pass validation, got: %v", concurrency, output.Err)
			}
			if !capture.called {
				t.Fatalf("expected workflow to be called for concurrency=%s", concurrency)
			}
		})
	}
}

func TestAddCmd_FlagsMapToOptions(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want add.Options
	}{
		{
			name: "minimal single image",
			args: []string{"nginx"},
			want: add.Options{Images: []string{"nginx"}, Concurrency: 4},
		},
		{
			name: "multiple images",
			args: []string{"nginx", "redis", "postgres"},
			want: add.Options{Images: []string{"nginx", "redis", "postgres"}, Concurrency: 4},
		},
		{
			name: "with output-dir",
			args: []string{"nginx", "--output-dir", "/tmp/output"},
			want: add.Options{Images: []string{"nginx"}, OutputDir: "/tmp/output", Concurrency: 4},
		},
		{
			name: "with output-dir shorthand",
			args: []string{"nginx", "-o", "/tmp/output"},
			want: add.Options{Images: []string{"nginx"}, OutputDir: "/tmp/output", Concurrency: 4},
		},
		{
			name: "with concurrency",
			args: []string{"nginx", "--concurrency", "8"},
			want: add.Options{Images: []string{"nginx"}, Concurrency: 8},
		},
		{
			name: "with concurrency shorthand",
			args: []string{"nginx", "-c", "8"},
			want: add.Options{Images: []string{"nginx"}, Concurrency: 8},
		},
		{
			name: "all flags",
			args: []string{
				"nginx:latest",
				"redis:7",
				"-o", "/tmp/custom",
				"-c", "8",
				"-V",
			},
			want: add.Options{
				Images:      []string{"nginx:latest", "redis:7"},
				OutputDir:   "/tmp/custom",
				Concurrency: 8,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			capture, restore := spyAddRun(nil)
			defer restore()

			output := ExecuteCommand(addCmd, tt.args)
			if output.Err != nil {
				t.Fatalf("unexpected error: %v\nstderr: %s", output.Err, output.Stderr)
			}
			if !capture.called {
				t.Fatal("expected workflow to be invoked, it was not")
			}
			if !reflect.DeepEqual(capture.opts, tt.want) {
				t.Fatalf("Options mismatch:\n got  %+v\n want %+v", capture.opts, tt.want)
			}
		})
	}
}

func TestAddCmd_WorkflowErrorPropagates(t *testing.T) {
	_, restore := spyAddRun(errors.New("boom"))
	defer restore()

	output := ExecuteCommand(addCmd, []string{"nginx"})
	if output.Err == nil {
		t.Fatalf("expected workflow error to propagate, got nil")
	}
	if !strings.Contains(output.Err.Error(), "boom") {
		t.Fatalf("expected workflow error to contain boom, got: %v", output.Err)
	}
}

func TestAddCmd_HelpFlag(t *testing.T) {
	output := ExecuteCommand(addCmd, []string{"--help"})
	if output.Err != nil {
		t.Fatalf("--help should not produce error: %v", output.Err)
	}
	helpOutput := output.Stdout + output.Stderr
	if !strings.Contains(helpOutput, "Add") || !strings.Contains(helpOutput, "image") {
		t.Fatalf("help output missing expected content: %s", helpOutput)
	}
}

func TestAddCmd_UnknownFlag(t *testing.T) {
	output := ExecuteCommand(addCmd, []string{"nginx", "--unknown-flag", "value"})
	if output.Err == nil {
		t.Fatalf("expected error for unknown flag, got none")
	}
}
