package pushcli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"helm-deep-pack/internal/push"
)

func TestNewCommand_MapsArgsAndFlagsToOptions(t *testing.T) {
	state := State{Concurrency: 4}
	var called bool
	var got push.Options

	cmd := NewCommand(Config{
		Use:   "push REGISTRY",
		State: &state,
		Run: func(_ context.Context, opts push.Options, _ ...io.Writer) error {
			called = true
			got = opts
			return nil
		},
	})
	cmd.SetIn(strings.NewReader(""))
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"registry.example.com/team", "-i", "/tmp/in", "-c", "8", "-a", "-k"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !called {
		t.Fatalf("expected run to be called")
	}
	if got.Registry != "registry.example.com/team" || got.InputDir != "/tmp/in" || got.Concurrency != 8 || !got.All || !got.AllowInsecureHTTP {
		t.Fatalf("unexpected options: %#v", got)
	}
}

func TestNewCommand_AllowsZeroArgs(t *testing.T) {
	state := State{Concurrency: 4}
	var called bool
	var got push.Options

	cmd := NewCommand(Config{
		Use:   "push [REGISTRY]",
		State: &state,
		Run: func(_ context.Context, opts push.Options, _ ...io.Writer) error {
			called = true
			got = opts
			return nil
		},
	})
	cmd.SetIn(strings.NewReader(""))
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !called {
		t.Fatalf("expected run to be called")
	}
	if got.Registry != "" {
		t.Fatalf("expected empty registry when omitted, got %q", got.Registry)
	}
}

func TestNewCommand_ValidatesInputs(t *testing.T) {
	state := State{Concurrency: 4}
	cmd := NewCommand(Config{
		Use:   "push REGISTRY",
		State: &state,
		Run: func(_ context.Context, _ push.Options, _ ...io.Writer) error {
			return nil
		},
	})
	var stderr bytes.Buffer
	cmd.SetOut(io.Discard)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"https://registry.example.com"})

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected error for invalid registry")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "registry") {
		t.Fatalf("error should mention registry, got %v", err)
	}
}

func TestNewCommand_PropagatesRunError(t *testing.T) {
	state := State{Concurrency: 4}
	cmd := NewCommand(Config{
		Use:   "push REGISTRY",
		State: &state,
		Run: func(_ context.Context, _ push.Options, _ ...io.Writer) error {
			return errors.New("boom")
		},
	})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"docker.io"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected propagated run error, got %v", err)
	}
}
