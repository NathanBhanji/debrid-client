// Command debrid is the debrid-client binary: server, CLI and MCP server in one.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/NathanBhanji/debrid-client/internal/buildinfo"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "debrid",
		Short:         "Debrid download manager: API server, CLI and MCP server",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newVersionCmd())
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
