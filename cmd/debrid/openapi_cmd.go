package main

import (
	"github.com/spf13/cobra"

	"github.com/NathanBhanji/debrid-client/internal/api"
)

func newOpenAPICmd() *cobra.Command {
	var yamlOut bool
	cmd := &cobra.Command{
		Use:   "openapi",
		Short: "Print the OpenAPI specification",
		RunE: func(cmd *cobra.Command, _ []string) error {
			h := api.New(nil, api.Options{})
			var b []byte
			var err error
			if yamlOut {
				b, err = h.Huma.OpenAPI().DowngradeYAML()
			} else {
				b, err = h.Huma.OpenAPI().Downgrade()
			}
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(b)
			return err
		},
	}
	cmd.Flags().BoolVar(&yamlOut, "yaml", false, "emit YAML instead of JSON (OpenAPI 3.0 downgrade)")
	return cmd
}
