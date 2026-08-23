package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/spf13/cobra"

	"github.com/NathanBhanji/debrid-client/internal/apiclient"
)

func newStatusCmd(g *globalFlags, cf *clientFlags) *cobra.Command {
	return &cobra.Command{
		Use: "status", Short: "Show server status",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl, err := cf.resolve(cmd, g)
			if err != nil {
				return err
			}
			resp, err := cl.GetStatusWithResponse(cmd.Context())
			if err != nil {
				return err
			}
			return cf.respond(cmd.OutOrStdout(), resp.StatusCode(), resp.Body, 200, func(w io.Writer) error {
				s := resp.JSON200
				_, _ = fmt.Fprintf(w, "version:      %s\naccounts:     %d\ndownload dir: %s\ndisk free:    %s of %s\n",
					s.Version, s.Accounts, s.DownloadDir, humanBytes(s.DiskFreeBytes), humanBytes(s.DiskTotalBytes))
				if len(s.Torrents) > 0 {
					_, _ = fmt.Fprintln(w, "torrents:")
					keys := make([]string, 0, len(s.Torrents))
					for k := range s.Torrents {
						keys = append(keys, k)
					}
					sort.Strings(keys)
					for _, k := range keys {
						_, _ = fmt.Fprintf(w, "  %-18s %d\n", k, s.Torrents[k])
					}
				}
				return nil
			})
		},
	}
}

func newSettingsCmd(g *globalFlags, cf *clientFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "settings", Short: "View or replace runtime settings"}
	get := &cobra.Command{
		Use: "get", Short: "Print settings as JSON",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl, err := cf.resolve(cmd, g)
			if err != nil {
				return err
			}
			resp, err := cl.GetSettingsWithResponse(cmd.Context())
			if err != nil {
				return err
			}
			return cf.respond(cmd.OutOrStdout(), resp.StatusCode(), resp.Body, 200, nil)
		},
	}
	set := &cobra.Command{
		Use:   "set [file.json|-]",
		Short: "Replace settings from a JSON file (or stdin)",
		Long:  "Reads a full settings document (as printed by `settings get`) and stores it.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := cmd.InOrStdin()
			if len(args) == 1 && args[0] != "-" {
				f, err := os.Open(args[0])
				if err != nil {
					return err
				}
				defer func() { _ = f.Close() }()
				r = f
			}
			var body apiclient.UpdateSettingsJSONRequestBody
			if err := json.NewDecoder(r).Decode(&body); err != nil {
				return fmt.Errorf("parse settings: %w", err)
			}
			cl, err := cf.resolve(cmd, g)
			if err != nil {
				return err
			}
			resp, err := cl.UpdateSettingsWithResponse(cmd.Context(), body)
			if err != nil {
				return err
			}
			return cf.respond(cmd.OutOrStdout(), resp.StatusCode(), resp.Body, 200, nil)
		},
	}
	cmd.AddCommand(get, set)
	return cmd
}

func newDownloadsCmd(g *globalFlags, cf *clientFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "downloads", Aliases: []string{"dl"}, Short: "Manage individual file downloads"}
	retry := &cobra.Command{
		Use: "retry <download-id>", Short: "Retry a failed download", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := cf.resolve(cmd, g)
			if err != nil {
				return err
			}
			resp, err := cl.RetryDownloadWithResponse(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return cf.respond(cmd.OutOrStdout(), resp.StatusCode(), resp.Body, 200, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "retrying %s (%s)\n", resp.JSON200.Id, resp.JSON200.State)
				return err
			})
		},
	}
	cmd.AddCommand(retry)
	return cmd
}
