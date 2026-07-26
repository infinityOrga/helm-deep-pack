package cmd

import (
	"context"
	"io"

	"github.com/spf13/cobra"

	"helm-deep-pack/internal/push"
	"helm-deep-pack/internal/pushcli"
)

const defaultPushConcurrency = 4

func newPushState() pushcli.State {
	return pushcli.State{
		Concurrency: defaultPushConcurrency,
	}
}

func newPushCommand(use string, state *pushcli.State) *cobra.Command {
	return pushcli.NewCommand(pushcli.Config{
		Use:   use,
		Short: "Push mirrored images from generated OCI layout artifacts",
		Run: func(ctx context.Context, opts push.Options, status ...io.Writer) error {
			return pushRun(ctx, opts, status...)
		},
		LoggerFactory: commandLogger,
		State:         state,
	})
}
