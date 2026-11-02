package cmd

import (
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"helm-deep-pack/internal/pull"
	"helm-deep-pack/internal/terminal"
	"helm-deep-pack/internal/termstyle"
	"helm-deep-pack/internal/validation"
)

var (
	pullChart                string
	pullRepo                 string
	pullVersion              string
	pullOutputDir            string
	pullConcurrency          int
	pullValuesFiles          []string
	pullSetValues            []string
	pullAllowHTTP            bool
	pullVerbose              bool
	pullDestPlatform         string
	pullRenderedOnly         bool
	pullNoOptional           bool
	pullOptionalImageTimeout time.Duration
)

var pullRun = pull.Run

const recommendedDestinationPlatform = "windows/amd64"

var pullCmd = &cobra.Command{
	Use:   "pull CHART",
	Short: "Render a chart and mirror its images",
	Long:  "Render a Helm chart and extract its container images. Supports local charts, HTTP(S) repositories, configured Helm repositories, and oci:// chart references. Use --no-optional to omit annotation-only and synthetic-discovery-only images from the output bundle.",
	Example: `  # Include Cilium Hubble Relay and UI without synthetic discovery:
  helm-deep-pack pull cilium --set hubble.enabled=true --set hubble.relay.enabled=true --set hubble.ui.enabled=true --no-optional`,
	Args: cobra.ExactArgs(1),
	PreRunE: func(cmd *cobra.Command, args []string) error {
		pullChart = args[0]

		chartIsOCI := validation.IsOCIReference(pullChart)
		chartIsLocal := validation.IsLocalChartPath(pullChart)
		repoIsOCI := validation.IsOCIReference(pullRepo)

		if chartIsOCI {
			if err := validation.ValidateOCIRef("chart argument", pullChart); err != nil {
				return err
			}
		} else if !chartIsLocal {
			if err := validation.ValidateChartName("chart argument", pullChart); err != nil {
				return err
			}
		}

		if pullRepo != "" {
			switch {
			case chartIsOCI:
				return fmt.Errorf("--repo cannot be combined with an oci:// chart reference")
			case chartIsLocal:
				return fmt.Errorf("--repo cannot be combined with a local chart path")
			}
			if repoIsOCI {
				if err := validation.ValidateOCIRef("--repo", pullRepo); err != nil {
					return err
				}
			} else {
				if err := validation.ValidateURL("--repo", pullRepo, pullAllowHTTP); err != nil {
					return err
				}
			}
		}
		if err := validation.ValidateConcurrency("--concurrency", pullConcurrency); err != nil {
			return err
		}
		if err := validation.ValidateNonNegativeDuration("--optional-image-timeout", pullOptionalImageTimeout); err != nil {
			return err
		}
		if pullDestPlatform != "" {
			if err := validation.ValidateDestinationPlatform("--destination-platform", pullDestPlatform); err != nil {
				return err
			}
			goos, goarch, err := validation.ParseDestinationPlatform(pullDestPlatform)
			if err != nil {
				return fmt.Errorf("normalize --destination-platform: %w", err)
			}
			pullDestPlatform = goos + "/" + goarch
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		effectiveDestination := resolveEffectiveDestination(pullDestPlatform)
		if err := warnIfNonRecommendedDestination(cmd, effectiveDestination); err != nil {
			return err
		}

		commandLogger(pullVerbose).Debug("pulling chart",
			"chart", pullChart,
			"repo", pullRepo,
			"version", pullVersion,
			"destination_platform", effectiveDestination,
			"concurrency", pullConcurrency,
			"values_files", len(pullValuesFiles),
			"set_overrides", len(pullSetValues),
			"no_optional", pullNoOptional,
		)

		return pullRun(cmd.Context(), pull.Options{
			Chart:                pullChart,
			Repo:                 pullRepo,
			Version:              pullVersion,
			OutputDir:            pullOutputDir,
			Concurrency:          pullConcurrency,
			ValuesFiles:          pullValuesFiles,
			SetValues:            pullSetValues,
			DestinationPlatform:  pullDestPlatform,
			HelperVersion:        Version(),
			RenderedOnly:         pullRenderedOnly,
			NoOptional:           pullNoOptional,
			OptionalImageTimeout: pullOptionalImageTimeout,
			Verbose:              pullVerbose,
		}, cmd.ErrOrStderr())
	},
}

func init() {
	pullCmd.Flags().StringVarP(&pullRepo, "repo", "r", "", "Helm repository URL (https or oci:// by default; optional; if not provided, searches configured Helm repositories)")
	pullCmd.Flags().StringVarP(&pullVersion, "version", "v", "", "Helm chart version")
	pullCmd.Flags().StringVarP(&pullOutputDir, "output-dir", "o", "", "Directory for OCI layout artifacts and script")
	pullCmd.Flags().IntVarP(&pullConcurrency, "concurrency", "c", 4, "Number of images to fetch and stage concurrently")
	pullCmd.Flags().StringArrayVarP(&pullValuesFiles, "values", "f", nil, "Load chart values to enable wanted features and include their images (can specify multiple files)")
	pullCmd.Flags().StringArrayVar(&pullSetValues, "set", nil, "Set chart values, e.g. feature.enabled=true, to include feature images (can specify multiple: key1=val1,key2=val2)")
	pullCmd.Flags().StringVar(&pullDestPlatform, "destination-platform", "", "Destination platform for staged push helper (os/arch, e.g. windows/amd64); defaults to current platform")
	pullCmd.Flags().BoolVarP(&pullAllowHTTP, "allow-insecure-http", "k", false, "Allow plaintext HTTP for Helm repository URLs")
	pullCmd.Flags().BoolVarP(&pullVerbose, "verbose", "V", false, "Show debug logs and detailed optional-image discovery diagnostics")
	pullCmd.Flags().BoolVar(&pullRenderedOnly, "rendered-only", false, "Skip synthetic discovery; keep selected-render and chart-annotation images")
	pullCmd.Flags().BoolVar(&pullNoOptional, "no-optional", false, "Only stage selected-render images; skip optional discovery and annotation-only images")
	pullCmd.Flags().DurationVar(&pullOptionalImageTimeout, "optional-image-timeout", pull.DefaultOptionalImageTimeout, "Time budget for best-effort optional image flag attribution (0 skips flag hints, not image discovery)")
}

func resolveEffectiveDestination(configured string) string {
	if configured != "" {
		return configured
	}
	return runtime.GOOS + "/" + runtime.GOARCH
}

func warnIfNonRecommendedDestination(cmd *cobra.Command, effectiveDestination string) error {
	if strings.EqualFold(effectiveDestination, recommendedDestinationPlatform) {
		return nil
	}

	warnOut := cmd.ErrOrStderr()
	yellow := ""
	reset := ""
	if terminal.IsWriter(warnOut) {
		yellow = termstyle.Yellow
		reset = termstyle.Reset
	}

	currentPlatform := runtime.GOOS + "/" + runtime.GOARCH
	if _, err := fmt.Fprintf(warnOut, "%swarning: destination platform is %q (host=%q), which diverges from the recommended %s target. To create a Windows x64 bundle, use --destination-platform windows/amd64.%s\n", yellow, effectiveDestination, currentPlatform, recommendedDestinationPlatform, reset); err != nil {
		return fmt.Errorf("write destination platform warning: %w", err)
	}
	return nil
}
