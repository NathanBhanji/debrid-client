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
			return respondJSON(cf, cmd.OutOrStdout(), resp.StatusCode(), resp.Body, 200, resp.JSON200, func(w io.Writer, s apiclient.Status) error {
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
	cmd := &cobra.Command{Use: "settings", Short: "View or update runtime settings"}
	var replace bool
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
		Short: "Update settings from a JSON document (file or stdin), merged over the current settings",
		Long: `Reads a settings document (any subset of what 'settings get' prints) and
applies it on top of the current settings, so omitted keys keep their values.
Pass --replace to treat the document as the complete settings instead.`,
		Args: cobra.MaximumNArgs(1),
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
			raw, err := io.ReadAll(r)
			if err != nil {
				return err
			}
			cl, err := cf.resolve(cmd, g)
			if err != nil {
				return err
			}
			var body apiclient.UpdateSettingsJSONRequestBody
			if !replace {
				cur, err := cl.GetSettingsWithResponse(cmd.Context())
				if err != nil {
					return err
				}
				if cur.StatusCode() != 200 || cur.JSON200 == nil {
					return apiError(cur.StatusCode(), cur.Body)
				}
				body = *cur.JSON200 // start from current, overlay the document
			}
			if err := json.Unmarshal(raw, &body); err != nil {
				return fmt.Errorf("parse settings: %w", err)
			}
			resp, err := cl.UpdateSettingsWithResponse(cmd.Context(), body)
			if err != nil {
				return err
			}
			return cf.respond(cmd.OutOrStdout(), resp.StatusCode(), resp.Body, 200, nil)
		},
	}
	set.Flags().BoolVar(&replace, "replace", false, "treat the document as the complete settings (omitted keys reset to zero values)")
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
			return respondJSON(cf, cmd.OutOrStdout(), resp.StatusCode(), resp.Body, 200, resp.JSON200, func(w io.Writer, d apiclient.Download) error {
				_, err := fmt.Fprintf(w, "retrying %s (%s)\n", d.Id, d.State)
				return err
			})
		},
	}
	cmd.AddCommand(retry)
	return cmd
}
