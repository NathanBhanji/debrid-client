// Command debrid is the debrid-client binary: server, CLI and MCP server in one.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/NathanBhanji/debrid-client/internal/buildinfo"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := newRootCmd().ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

func newRootCmd() *cobra.Command {
	g := &globalFlags{}
	root := &cobra.Command{
		Use:           "debrid",
		Short:         "Debrid download manager: API server, CLI and MCP server",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&g.configFile, "config", "", "config file (default: $DEBRID_CONFIG, else $XDG_CONFIG_HOME/debrid/config.yaml)")
	cf := &clientFlags{}
	root.AddCommand(newVersionCmd(), newConfigCmd(g), newServeCmd(g), newOpenAPICmd())
	// Client commands get --server/--api-key/--json; serve/config don't (they'd be silently ignored).
	for _, c := range []*cobra.Command{newStatusCmd(g, cf), newTorrentsCmd(g, cf), newAccountsCmd(g, cf), newDownloadsCmd(g, cf), newSettingsCmd(g, cf)} {
		cf.bind(c)
		root.AddCommand(c)
	}
	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "debrid "+buildinfo.String())
			return err
		},
	}
}
