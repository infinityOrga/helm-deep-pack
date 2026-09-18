package cmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"helm-deep-pack/internal/add"
	"helm-deep-pack/internal/pull"
	"helm-deep-pack/internal/push"
	"helm-deep-pack/internal/validation"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type CapturedOutput struct {
	Stdout string
	Stderr string
	Err    error
}

func ExecuteCommand(cmd *cobra.Command, args []string) *CapturedOutput {
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)

	execCmd := cmd
	targetCmd := cmd.Name()
	if cmd.Name() != "helm-deep-pack" && cmd.Name() != "" {
		execCmd = rootCmd
		args = append([]string{cmd.Name()}, args...)
	}
	if targetCmd == "helm-deep-pack" && len(args) > 0 {
		targetCmd = args[0]
	}
	if targetCmd == "pull" || targetCmd == "push" || targetCmd == "add" {
		resetCmdVars(targetCmd)
		defer resetCmdVars(targetCmd)
	}
	if targetCmd == "upgrade" {
		resetCmdVars(targetCmd)
		defer resetCmdVars(targetCmd)
	}

	origOut := execCmd.OutOrStdout()
	origErr := execCmd.ErrOrStderr()
	flagSets := []*pflag.FlagSet{
		rootCmd.Flags(),
		rootCmd.PersistentFlags(),
		execCmd.Flags(),
		execCmd.PersistentFlags(),
	}
	for _, flagSet := range flagSets {
		if flagSet == nil {
			continue
		}
		if flag := flagSet.Lookup("version"); flag != nil {
			_ = flag.Value.Set("false")
			flag.Changed = false
		}
	}

	execCmd.SetOut(stdout)
	execCmd.SetErr(stderr)
	execCmd.SetIn(new(bytes.Buffer))
	execCmd.SetArgs(args)
	err := execCmd.Execute()
	execCmd.SetOut(origOut)
	execCmd.SetErr(origErr)

	return &CapturedOutput{Stdout: stdout.String(), Stderr: stderr.String(), Err: err}
}

func AssertFlagExists(t *testing.T, cmd *cobra.Command, flagName string) {
	t.Helper()
	if cmd.Flags().Lookup(flagName) == nil {
		t.Fatalf("expected flag %q to exist on command %q", flagName, cmd.Name())
	}
}

func AssertFlagNotExists(t *testing.T, cmd *cobra.Command, flagName string) {
	t.Helper()
	if cmd.Flags().Lookup(flagName) != nil {
		t.Fatalf("expected flag %q to NOT exist on command %q", flagName, cmd.Name())
	}
}

func AssertFlagType(t *testing.T, cmd *cobra.Command, flagName, expectedType string) {
	t.Helper()
	flag := cmd.Flags().Lookup(flagName)
	if flag == nil {
		t.Fatalf("flag %q not found", flagName)
	}
	actualType := flag.Value.Type()
	if actualType != expectedType {
		t.Fatalf("flag %q: expected type %q, got %q", flagName, expectedType, actualType)
	}
}

func AssertFlagShorthand(t *testing.T, cmd *cobra.Command, flagName, expectedShorthand string) {
	t.Helper()
	flag := cmd.Flags().Lookup(flagName)
	if flag == nil {
		t.Fatalf("flag %q not found", flagName)
	}
	if flag.Shorthand != expectedShorthand {
		t.Fatalf("flag %q: expected shorthand %q, got %q", flagName, expectedShorthand, flag.Shorthand)
	}
}

func AssertFlagDefault(t *testing.T, cmd *cobra.Command, flagName, expectedDefault string) {
	t.Helper()
	flag := cmd.Flags().Lookup(flagName)
	if flag == nil {
		t.Fatalf("flag %q not found", flagName)
	}
	actualDefault := flag.Value.String()
	if actualDefault != expectedDefault {
		t.Fatalf("flag %q: expected default %q, got %q", flagName, expectedDefault, actualDefault)
	}
}

func combinedErrorText(output *CapturedOutput) string {
	text := output.Stderr + output.Stdout
	if output.Err != nil {
		text += output.Err.Error()
	}
	return strings.ToLower(text)
}

type pullCapture struct {
	called bool
	opts   pull.Options
}

func spyPullRun(retErr error) (*pullCapture, func()) {
	resetCmdVars("pull")
	capture := &pullCapture{}
	orig := pullRun
	pullRun = func(_ context.Context, opts pull.Options, _ ...io.Writer) error {
		capture.called = true
		capture.opts = opts
		return retErr
	}

	return capture, func() {
		pullRun = orig
		resetCmdVars("pull")
	}
}

type addCapture struct {
	called bool
	opts   add.Options
}

func spyAddRun(retErr error) (*addCapture, func()) {
	resetCmdVars("add")
	capture := &addCapture{}
	orig := addRun
	addRun = func(_ context.Context, opts add.Options, _ ...io.Writer) error {
		capture.called = true
		capture.opts = opts
		return retErr
	}

	return capture, func() {
		addRun = orig
		resetCmdVars("add")
	}
}

type pushCapture struct {
	called bool
	opts   push.Options
}

func spyPushRun(retErr error) (*pushCapture, func()) {
	resetCmdVars("push")
	capture := &pushCapture{}
	orig := pushRun
	pushRun = func(_ context.Context, opts push.Options, _ ...io.Writer) error {
		capture.called = true
		capture.opts = opts
		return retErr
	}

	return capture, func() {
		pushRun = orig
		resetCmdVars("push")
	}
}

func resetCmdVars(cmdName string) {
	switch cmdName {
	case "pull":
		pullChart = ""
		pullRepo = ""
		pullVersion = ""
		pullOutputDir = ""
		pullConcurrency = 4
		pullValuesFiles = nil
		pullSetValues = nil
		pullDestPlatform = ""
		pullAllowHTTP = false
		pullVerbose = false
		pullRenderedOnly = false
		pullOptionalImageTimeout = pull.DefaultOptionalImageTimeout
	case "add":
		addImages = nil
		addOutputDir = ""
		addConcurrency = 4
		addVerbose = false
	case "push":
		pushState.Registry = ""
		pushState.InputDir = ""
		pushState.Concurrency = 4
		pushState.All = false
		pushState.AllowHTTP = false
		pushState.Verbose = false
	case "upgrade":
		upgradeTargetVersion = ""
		upgradeForce = false
		upgradeAssumeYes = false
		upgradeVerbose = false
	default:
		panic("resetCmdVars: unknown command " + cmdName)
	}
}

func validConcurrencyValues() []string {
	values := []string{"1"}
	for _, value := range []int{4, 8, 16} {
		if validation.ValidateConcurrency("--concurrency", value) == nil {
			values = append(values, fmt.Sprintf("%d", value))
		}
	}
	return values
}

func CleanupChartDirs() {
	cwd, err := os.Getwd()
	if err != nil {
		return
	}

	entries, err := os.ReadDir(cwd)
	if err != nil {
		return
	}

	chartNames := map[string]bool{
		"nginx": true, "openebs": true, "prometheus": true,
		"redis": true, "mysql": true, "postgresql": true,
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		name := entry.Name()
		isChartDir := false
		for chart := range chartNames {
			if name == chart || strings.HasPrefix(name, chart+"-") {
				if strings.HasPrefix(name, chart+"-") {
					rest := strings.TrimPrefix(name, chart+"-")
					if len(rest) == 13 && rest[4] == '-' && rest[7] == '-' && rest[10] == '-' {
						isChartDir = true
						break
					}
				} else {
					isChartDir = true
					break
				}
			}
		}

		if isChartDir {
			_ = os.RemoveAll(filepath.Join(cwd, name))
		}
	}
}
