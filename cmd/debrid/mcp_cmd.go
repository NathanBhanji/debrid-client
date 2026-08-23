package main

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/NathanBhanji/debrid-client/internal/mcpserver"
)

func newMCPCmd(g *globalFlags, cf *clientFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Run an MCP server over stdio that controls a running debrid-client",
		Long: `Speaks the Model Context Protocol on stdin/stdout for use by MCP clients
(Claude Desktop, Claude Code, Cursor, ...). Tool calls are forwarded to the
debrid-client API server selected by --server/--api-key (or their defaults).

The running server also exposes the same tools over Streamable HTTP at
<server>/mcp (authenticate with the API key as a Bearer token).

Example client config:
  {"mcpServers": {"debrid": {"command": "debrid", "args": ["mcp"], "env": {"DEBRID_API_KEY": "..."}}}}`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl, err := cf.resolve(cmd, g)
			if err != nil {
				return err
			}
			return mcpserver.New(cl).Run(cmd.Context(), &mcp.StdioTransport{})
		},
	}
}
