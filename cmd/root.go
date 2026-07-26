package cmd

import (
	"helm-deep-pack/internal/push"
	"helm-deep-pack/internal/upgrade"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var version = "dev"

// commandLogger returns a stderr logger; verbose enables debug-level output.
func commandLogger(verbose bool) *slog.Logger {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:     "helm-deep-pack",
	Short:   "Mirror Helm chart images into OCI layout artifacts",
	Version: version,
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	upgrade.CleanupStale()
	handled, err := runPushHelperIfNeeded(os.Args, os.Stdin, os.Stdout, os.Stderr)
	if err != nil {
		os.Exit(1)
	}
	if handled {
		return
	}
	err = rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func Version() string {
	return version
}

func init() {
	rootCmd.SetVersionTemplate("helm-deep-pack {{.Version}}\n")
	rootCmd.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		cmd.SilenceUsage = true
	}
	rootCmd.AddCommand(pullCmd)
	rootCmd.AddCommand(addCmd)
	rootCmd.AddCommand(pushCmd)
	rootCmd.AddCommand(upgradeCmd)
	rootCmd.AddCommand(upgradeHelperCmd)
}

func runPushHelperIfNeeded(args []string, in io.Reader, out, errOut io.Writer) (bool, error) {
	if len(args) == 0 || !isPushHelperInvocation(args[0]) {
		return false, nil
	}
	helperArgs := args[1:]
	if len(helperArgs) > 0 && strings.EqualFold(helperArgs[0], "push") {
		helperArgs = helperArgs[1:]
	}

	helperState := newPushState()
	cmd := newPushCommand("push_images [REGISTRY]", &helperState)
	cmd.SilenceUsage = true
	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs(helperArgs)
	if err := cmd.Execute(); err != nil {
		return true, err
	}
	return true, nil
}

func isPushHelperInvocation(argv0 string) bool {
	normalizedArgv0 := strings.ReplaceAll(argv0, "\\", "/")
	base := strings.ToLower(filepath.Base(normalizedArgv0))
	expected := strings.ToLower(push.PushBinaryName())
	if base == expected {
		return true
	}
	return strings.TrimSuffix(base, filepath.Ext(base)) == strings.TrimSuffix(expected, filepath.Ext(expected))
}
