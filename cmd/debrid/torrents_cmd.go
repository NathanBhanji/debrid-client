package main

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/NathanBhanji/debrid-client/internal/apiclient"
	"github.com/NathanBhanji/debrid-client/internal/torrentmeta"
)

func newTorrentsCmd(g *globalFlags, cf *clientFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "torrents", Aliases: []string{"t", "torrent"}, Short: "Manage torrents"}

	var addAccount, addCategory string
	add := &cobra.Command{
		Use:   "add <magnet|file.torrent> [...]",
		Short: "Add torrents from magnet links or .torrent files",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := cf.resolve(cmd, g)
			if err != nil {
				return err
			}
			for _, a := range args {
				if torrentmeta.IsMagnet(a) {
					body := apiclient.AddTorrentJSONRequestBody{Magnet: a}
					if addAccount != "" {
						body.Account = &addAccount
					}
					if addCategory != "" {
						body.Category = &addCategory
					}
					resp, err := cl.AddTorrentWithResponse(cmd.Context(), body)
					if err != nil {
						return err
					}
					if err := cf.respond(cmd.OutOrStdout(), resp.StatusCode(), resp.Body, 201, func(w io.Writer) error {
						_, err := fmt.Fprintf(w, "added %s  %s  (%s)\n", resp.JSON201.Id, resp.JSON201.Name, resp.JSON201.Status)
						return err
					}); err != nil {
						return err
					}
					continue
				}
				data, err := os.ReadFile(a)
				if err != nil {
					return err
				}
				var buf bytes.Buffer
				mw := multipart.NewWriter(&buf)
				fw, err := mw.CreateFormFile("file", a)
				if err != nil {
					return err
				}
				_, _ = fw.Write(data)
				if addAccount != "" {
					_ = mw.WriteField("account", addAccount)
				}
				if addCategory != "" {
					_ = mw.WriteField("category", addCategory)
				}
				_ = mw.Close()
				resp, err := cl.AddTorrentFileWithBodyWithResponse(cmd.Context(), mw.FormDataContentType(), &buf)
				if err != nil {
					return err
				}
				if err := cf.respond(cmd.OutOrStdout(), resp.StatusCode(), resp.Body, 201, func(w io.Writer) error {
					_, err := fmt.Fprintf(w, "added %s  %s  (%s)\n", resp.JSON201.Id, resp.JSON201.Name, resp.JSON201.Status)
					return err
				}); err != nil {
					return err
				}
			}
			return nil
		},
	}
	add.Flags().StringVar(&addAccount, "account", "", "account id or name (default: the default account)")
	add.Flags().StringVarP(&addCategory, "category", "c", "", "category (sub-folder)")

	var lsStatus, lsAccount, lsCategory string
	var lsWatch bool
	ls := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List torrents",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl, err := cf.resolve(cmd, g)
			if err != nil {
				return err
			}
			for {
				params := &apiclient.ListTorrentsParams{}
				if lsStatus != "" {
					params.Status = ptr(apiclient.ListTorrentsParamsStatus(lsStatus))
				}
				if lsAccount != "" {
					params.Account = &lsAccount
				}
				if lsCategory != "" {
					params.Category = &lsCategory
				}
				resp, err := cl.ListTorrentsWithResponse(cmd.Context(), params)
				if err != nil {
					return err
				}
				if lsWatch && !cf.json {
					_, _ = fmt.Fprint(cmd.OutOrStdout(), "\033[H\033[2J")
				}
				if err := cf.respond(cmd.OutOrStdout(), resp.StatusCode(), resp.Body, 200, func(w io.Writer) error {
					return renderTorrents(w, *resp.JSON200)
				}); err != nil {
					return err
				}
				if !lsWatch {
					return nil
				}
				select {
				case <-cmd.Context().Done():
					return nil
				case <-time.After(2 * time.Second):
				}
			}
		},
	}
	ls.Flags().StringVar(&lsStatus, "status", "", "filter by status")
	ls.Flags().StringVar(&lsAccount, "account", "", "filter by account id or name")
	ls.Flags().StringVarP(&lsCategory, "category", "c", "", "filter by category")
	ls.Flags().BoolVarP(&lsWatch, "watch", "w", false, "refresh every 2s")

	get := &cobra.Command{
		Use:   "get <id|hash>",
		Short: "Show a torrent with its files and downloads",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := cf.resolve(cmd, g)
			if err != nil {
				return err
			}
			resp, err := cl.GetTorrentWithResponse(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return cf.respond(cmd.OutOrStdout(), resp.StatusCode(), resp.Body, 200, func(w io.Writer) error {
				return renderTorrentDetail(w, *resp.JSON200)
			})
		},
	}

	var rmFiles, rmProvider bool
	rm := &cobra.Command{
		Use:     "rm <id|hash> [...]",
		Aliases: []string{"delete", "remove"},
		Short:   "Delete torrents",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := cf.resolve(cmd, g)
			if err != nil {
				return err
			}
			for _, id := range args {
				resp, err := cl.DeleteTorrentWithResponse(cmd.Context(), id, &apiclient.DeleteTorrentParams{Files: &rmFiles, Provider: &rmProvider})
				if err != nil {
					return err
				}
				if err := cf.respond(cmd.OutOrStdout(), resp.StatusCode(), resp.Body, 204, func(w io.Writer) error {
					_, err := fmt.Fprintln(w, "deleted", id)
					return err
				}); err != nil {
					return err
				}
			}
			return nil
		},
	}
	rm.Flags().BoolVar(&rmFiles, "files", false, "also delete downloaded files")
	rm.Flags().BoolVar(&rmProvider, "provider", false, "also delete at the debrid provider")

	retry := &cobra.Command{
		Use:   "retry <id|hash>",
		Short: "Retry an errored or completed torrent from scratch",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := cf.resolve(cmd, g)
			if err != nil {
				return err
			}
			resp, err := cl.RetryTorrentWithResponse(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return cf.respond(cmd.OutOrStdout(), resp.StatusCode(), resp.Body, 200, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "retrying %s (%s)\n", resp.JSON200.Id, resp.JSON200.Status)
				return err
			})
		},
	}

	sel := &cobra.Command{
		Use:   "select <id|hash> <file-id> [...]",
		Short: "Download only the given provider file ids",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := cf.resolve(cmd, g)
			if err != nil {
				return err
			}
			resp, err := cl.SelectFilesWithResponse(cmd.Context(), args[0], apiclient.SelectFilesJSONRequestBody{FileIds: args[1:]})
			if err != nil {
				return err
			}
			return cf.respond(cmd.OutOrStdout(), resp.StatusCode(), resp.Body, 200, func(w io.Writer) error {
				return renderTorrentDetail(w, *resp.JSON200)
			})
		},
	}

	var setCategory string
	set := &cobra.Command{
		Use:   "set <id|hash>",
		Short: "Update a torrent's category",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := cf.resolve(cmd, g)
			if err != nil {
				return err
			}
			body := apiclient.UpdateTorrentJSONRequestBody{}
			if cmd.Flags().Changed("category") {
				body.Category = &setCategory
			}
			resp, err := cl.UpdateTorrentWithResponse(cmd.Context(), args[0], body)
			if err != nil {
				return err
			}
			return cf.respond(cmd.OutOrStdout(), resp.StatusCode(), resp.Body, 200, func(w io.Writer) error {
				return renderTorrentDetail(w, *resp.JSON200)
			})
		},
	}
	set.Flags().StringVarP(&setCategory, "category", "c", "", "new category")

	cmd.AddCommand(add, ls, get, rm, retry, sel, set)
	return cmd
}

func renderTorrents(w io.Writer, ts []apiclient.Torrent) error {
	rows := make([][]string, 0, len(ts))
	for _, t := range ts {
		rows = append(rows, []string{shortID(t.Id), string(t.Status), pct(t.ProviderProgress), pct(t.LocalProgress), humanBytes(t.Size), deref(t.Category), t.Name, firstLine(deref(t.Error), deref(t.StatusReason))})
	}
	return table(w, []string{"ID", "STATUS", "PROV", "LOCAL", "SIZE", "CATEGORY", "NAME", "DETAIL"}, rows)
}

func renderTorrentDetail(w io.Writer, t apiclient.Torrent) error {
	_, _ = fmt.Fprintf(w, "%s  %s\n", t.Name, t.Id)
	_, _ = fmt.Fprintf(w, "  hash:      %s\n  status:    %s  %s\n  provider:  %.0f%%  local: %.0f%%  size: %s\n",
		t.Hash, t.Status, deref(t.StatusReason), t.ProviderProgress*100, t.LocalProgress*100, humanBytes(t.Size))
	if c := deref(t.Category); c != "" {
		_, _ = fmt.Fprintf(w, "  category:  %s\n", c)
	}
	if e := deref(t.Error); e != "" {
		_, _ = fmt.Fprintf(w, "  error:     %s\n", e)
	}
	if len(t.Files) > 0 {
		_, _ = fmt.Fprintln(w, "  files:")
		rows := make([][]string, 0, len(t.Files))
		for _, f := range t.Files {
			sel := ""
			if f.Selected {
				sel = "*"
			}
			rows = append(rows, []string{"    " + f.Id, sel, humanBytes(f.Size), f.Path})
		}
		if err := table(w, []string{"    FILE", "SEL", "SIZE", "PATH"}, rows); err != nil {
			return err
		}
	}
	if len(t.Downloads) > 0 {
		_, _ = fmt.Fprintln(w, "  downloads:")
		rows := make([][]string, 0, len(t.Downloads))
		for _, d := range t.Downloads {
			rows = append(rows, []string{"    " + shortID(d.Id), string(d.State), pct(d.Progress), humanBytes(d.Size), d.Path, deref(d.Error)})
		}
		return table(w, []string{"    ID", "STATE", "PROG", "SIZE", "PATH", "ERROR"}, rows)
	}
	return nil
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func firstLine(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			if i := strings.IndexByte(s, '\n'); i >= 0 {
				s = s[:i]
			}
			if len(s) > 60 {
				s = s[:57] + "..."
			}
			return s
		}
	}
	return ""
}
