package main

import (
	"context"
	"io"
	"log/slog"
	"os"

	"helm-deep-pack/internal/push"
	"helm-deep-pack/internal/pushcli"
)

var version = "dev"

func logger(verbose bool) *slog.Logger {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}

func main() {
	cmd := pushcli.NewCommand(pushcli.Config{
		Use:           "push_images [REGISTRY]",
		Short:         "Push staged images and the bundled Helm chart",
		Version:       version,
		VersionFormat: "push_images {{.Version}}\n",
		Run: func(ctx context.Context, opts push.Options, status ...io.Writer) error {
			return push.PushImages(ctx, opts, status...)
		},
		LoggerFactory: logger,
		State: &pushcli.State{
			Concurrency: 4,
		},
	})
	cmd.SilenceUsage = true
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
