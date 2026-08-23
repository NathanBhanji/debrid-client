package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/NathanBhanji/debrid-client/internal/app"
	"github.com/NathanBhanji/debrid-client/internal/config"
)

func newServeCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the API server and download engine",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := g.loadConfig(cmd)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			a, err := app.New(ctx, cfg, nil)
			if err != nil {
				return err
			}
			return a.Run(ctx) // Run closes the store on every exit path
		},
	}
	config.BindFlags(cmd.Flags())
	return cmd
}
