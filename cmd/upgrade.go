package cmd

import (
	"github.com/spf13/cobra"

	"helm-deep-pack/internal/upgrade"
)

var (
	upgradeTargetVersion string
	upgradeForce         bool
	upgradeAssumeYes     bool
	upgradeVerbose       bool
)

var upgradeRun = upgrade.Run

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Update this binary to the latest stable release",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		logger := commandLogger(upgradeVerbose)
		logger.Debug("upgrading binary",
			"target_version", upgradeTargetVersion,
			"force", upgradeForce,
		)

		return upgradeRun(cmd.Context(), upgrade.Options{
			CurrentVersion: Version(),
			TargetVersion:  upgradeTargetVersion,
			Force:          upgradeForce,
			AssumeYes:      upgradeAssumeYes,
			In:             cmd.InOrStdin(),
			Out:            cmd.OutOrStdout(),
			Logger:         logger,
		}, cmd.ErrOrStderr())
	},
}

func init() {
	upgradeCmd.Flags().StringVar(&upgradeTargetVersion, "version", "", "Target release version (default: latest stable)")
	upgradeCmd.Flags().BoolVar(&upgradeForce, "force", false, "Reinstall even if already on the target version")
	upgradeCmd.Flags().BoolVarP(&upgradeAssumeYes, "yes", "y", false, "Skip confirmation prompt")
	upgradeCmd.Flags().BoolVar(&upgradeVerbose, "verbose", false, "Enable verbose logging")
}
