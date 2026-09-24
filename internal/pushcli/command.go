package pushcli

import (
	"context"
	"io"
	"log/slog"

	"github.com/spf13/cobra"

	"helm-deep-pack/internal/push"
	"helm-deep-pack/internal/validation"
)

type State struct {
	Registry     string
	InputDir     string
	Concurrency  int
	All          bool
	IncludeChart bool
	AllowHTTP    bool
	Verbose      bool
}

type Runner func(ctx context.Context, opts push.Options, status ...io.Writer) error

type Config struct {
	Use           string
	Short         string
	Version       string
	VersionFormat string
	LoggerFactory func(verbose bool) *slog.Logger
	Run           Runner
	State         *State
}

func NewCommand(cfg Config) *cobra.Command {
	state := cfg.State
	if state == nil {
		state = &State{}
	}
	if state.Concurrency == 0 {
		state.Concurrency = 4
	}

	run := cfg.Run
	if run == nil {
		run = push.PushImages
	}

	use := cfg.Use
	if use == "" {
		use = "push [REGISTRY]"
	}
	short := cfg.Short
	if short == "" {
		short = "Push mirrored images from generated OCI layout artifacts"
	}

	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Long:  "REGISTRY may include a namespace path, which prefixes image destinations. Use --include-chart to also publish the bundled chart as an OCI artifact at REGISTRY/CHART_NAME:CHART_VERSION.",
		// The registry is optional: when omitted, the workflow prompts for it in
		// an interactive terminal and otherwise fails fast.
		Args: cobra.MaximumNArgs(1),
		PreRunE: func(cmd *cobra.Command, args []string) error {
			state.Registry = ""
			if len(args) == 1 {
				state.Registry = args[0]
				if err := validation.ValidateImageRegistryWithPath("registry argument", state.Registry); err != nil {
					return err
				}
			}
			if err := validation.ValidateConcurrency("--concurrency", state.Concurrency); err != nil {
				return err
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if cfg.LoggerFactory != nil {
				cfg.LoggerFactory(state.Verbose).Debug("pushing images to registry",
					"registry", state.Registry,
					"concurrency", state.Concurrency,
				)
			}

			return run(cmd.Context(), push.Options{
				Registry:          state.Registry,
				InputDir:          state.InputDir,
				Concurrency:       state.Concurrency,
				All:               state.All,
				IncludeChart:      state.IncludeChart,
				AllowInsecureHTTP: state.AllowHTTP,
				In:                cmd.InOrStdin(),
				Out:               cmd.OutOrStdout(),
			}, cmd.ErrOrStderr())
		},
	}

	cmd.Flags().StringVarP(&state.InputDir, "input-dir", "i", "", "Directory containing push_images.json and OCI layout artifacts")
	cmd.Flags().IntVarP(&state.Concurrency, "concurrency", "c", state.Concurrency, "Number of images to push concurrently")
	cmd.Flags().BoolVarP(&state.All, "all", "a", false, "Push all images without interactive selection")
	cmd.Flags().BoolVar(&state.IncludeChart, "include-chart", false, "Also publish the bundled Helm chart as an OCI artifact")
	cmd.Flags().BoolVarP(&state.AllowHTTP, "allow-insecure-http", "k", false, "Allow plaintext HTTP for registry probes and pushes")
	cmd.Flags().BoolVarP(&state.Verbose, "verbose", "V", false, "Enable verbose logging")

	if cfg.Version != "" {
		cmd.Version = cfg.Version
		if cfg.VersionFormat != "" {
			cmd.SetVersionTemplate(cfg.VersionFormat)
		}
	}

	return cmd
}
