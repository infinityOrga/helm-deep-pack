package push

import (
	"bytes"
	"strings"
	"testing"
)

func TestReadRegistryLoop(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"valid first try", "docker.io\n", "docker.io", false},
		{"valid no trailing newline", "docker.io", "docker.io", false},
		{"valid with namespace path", "registry.example.com:5000/team/sub\n", "registry.example.com:5000/team/sub", false},
		{"trims surrounding whitespace", "  docker.io  \n", "docker.io", false},
		{"empty line cancels", "\n", "", true},
		{"eof cancels", "", "", true},
		{"invalid then eof cancels", "https://bad\n", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := new(bytes.Buffer)
			got, err := readRegistryLoop(strings.NewReader(tt.input), out)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got registry %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("registry mismatch: got %q want %q", got, tt.want)
			}
			if !strings.Contains(out.String(), "Target registry:") {
				t.Fatalf("expected a registry prompt in output, got: %q", out.String())
			}
		})
	}
}

func TestReadRegistryLoop_RepromptsOnInvalid(t *testing.T) {
	out := new(bytes.Buffer)
	got, err := readRegistryLoop(strings.NewReader("https://nope\nquay.io\n"), out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "quay.io" {
		t.Fatalf("expected quay.io after re-prompt, got %q", got)
	}
	if !strings.Contains(out.String(), "invalid registry") {
		t.Fatalf("expected an invalid-registry notice before re-prompting, got: %q", out.String())
	}
}

func TestPromptForRegistry_NonInteractive(t *testing.T) {
	// Plain buffers are not terminals, so prompting must fail fast rather than
	// block on a read.
	if _, err := promptForRegistry(new(bytes.Buffer), new(bytes.Buffer)); err == nil {
		t.Fatal("expected error when input/output are not terminals")
	}
}
