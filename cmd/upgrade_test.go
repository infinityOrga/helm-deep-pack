package cmd

import (
	"bytes"
	"context"
	"errors"
	"helm-deep-pack/internal/upgrade"
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestUpgradeCmd_FlagsRegistered(t *testing.T) {
	flags := []string{"version", "force", "yes", "verbose"}
	for _, flag := range flags {
		AssertFlagExists(t, upgradeCmd, flag)
	}
}

func TestUpgradeCmd_FlagsNotExposed(t *testing.T) {
	flags := []string{"registry", "repo", "output-dir", "concurrency", "all", "input-dir"}
	for _, flag := range flags {
		AssertFlagNotExists(t, upgradeCmd, flag)
	}
}

func TestUpgradeCmd_FlagTypes(t *testing.T) {
	tests := map[string]string{
		"version": "string",
		"force":   "bool",
		"yes":     "bool",
		"verbose": "bool",
	}
	for flagName, expectedType := range tests {
		AssertFlagType(t, upgradeCmd, flagName, expectedType)
	}
}

func TestUpgradeCmd_FlagDefaults(t *testing.T) {
	tests := map[string]string{
		"version": "",
		"force":   "false",
		"yes":     "false",
		"verbose": "false",
	}
	for flagName, expectedDefault := range tests {
		AssertFlagDefault(t, upgradeCmd, flagName, expectedDefault)
	}
}

func TestUpgradeCmd_RunEWiresOptions(t *testing.T) {
	orig := upgradeRun
	defer func() {
		upgradeRun = orig
		resetCmdVars("upgrade")
	}()

	called := false
	var capturedOpts upgrade.Options
	var capturedErrWriters int
	upgradeRun = func(ctx context.Context, opts upgrade.Options, errWriters ...io.Writer) error {
		called = true
		capturedOpts = opts
		capturedErrWriters = len(errWriters)
		return nil
	}

	upgradeTargetVersion = "1.2.3"
	upgradeForce = true
	upgradeAssumeYes = true

	testCmd := &cobra.Command{}
	testCmd.SetIn(strings.NewReader(""))
	testCmd.SetOut(io.Discard)
	testCmd.SetErr(io.Discard)

	if err := upgradeCmd.RunE(testCmd, nil); err != nil {
		t.Fatalf("RunE() error = %v", err)
	}
	if !called {
		t.Fatal("expected upgrade workflow to be called")
	}
	if capturedOpts.TargetVersion != "1.2.3" {
		t.Fatalf("target version = %q, want %q", capturedOpts.TargetVersion, "1.2.3")
	}
	if !capturedOpts.Force || !capturedOpts.AssumeYes {
		t.Fatalf("unexpected opts: %+v", capturedOpts)
	}
	if capturedOpts.CurrentVersion == "" {
		t.Fatal("expected current version to be set")
	}
	if capturedErrWriters != 1 {
		t.Fatalf("err writer count = %d, want 1", capturedErrWriters)
	}
}

func TestUpgradeCmd_PropagatesWorkflowError(t *testing.T) {
	orig := upgradeRun
	defer func() {
		upgradeRun = orig
		resetCmdVars("upgrade")
	}()

	upgradeRun = func(_ context.Context, _ upgrade.Options, _ ...io.Writer) error {
		return errors.New("boom")
	}
	upgradeAssumeYes = true

	testCmd := &cobra.Command{}
	testCmd.SetIn(strings.NewReader(""))
	testCmd.SetOut(&bytes.Buffer{})
	testCmd.SetErr(io.Discard)

	err := upgradeCmd.RunE(testCmd, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected boom error, got: %v", err)
	}
}
