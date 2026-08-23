package main

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/NathanBhanji/debrid-client/internal/apiclient"
)

func newAccountsCmd(g *globalFlags, cf *clientFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "accounts", Aliases: []string{"account", "providers", "a"}, Short: "Manage provider accounts"}

	var kind, name, apiKey string
	var setDefault bool
	add := &cobra.Command{
		Use:     "add",
		Short:   "Add a provider account (credentials are verified with the provider)",
		Example: "  debrid accounts add --kind torbox --key $TORBOX_API_KEY\n  debrid accounts add --kind torbox --name second --key ... --default",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if kind == "" || apiKey == "" {
				return fmt.Errorf("--kind and --key are required")
			}
			cl, err := cf.resolve(cmd, g)
			if err != nil {
				return err
			}
			body := apiclient.AddAccountJSONRequestBody{Kind: apiclient.AddAccountInBodyKind(kind), Credentials: apiclient.Credentials{ApiKey: &apiKey}}
			if name != "" {
				body.Name = &name
			}
			if setDefault {
				body.SetDefault = &setDefault
			}
			resp, err := cl.AddAccountWithResponse(cmd.Context(), body)
			if err != nil {
				return err
			}
			return cf.respond(cmd.OutOrStdout(), resp.StatusCode(), resp.Body, 201, func(w io.Writer) error {
				return renderAccounts(w, []apiclient.Account{*resp.JSON201})
			})
		},
	}
	add.Flags().StringVar(&kind, "kind", "", "provider kind: torbox|realdebrid|alldebrid|premiumize|debridlink")
	add.Flags().StringVar(&name, "name", "", "display name (default: the kind)")
	add.Flags().StringVar(&apiKey, "key", "", "provider API key (not the debrid-client API key)")
	add.Flags().BoolVar(&setDefault, "default", false, "make this the default account")

	ls := &cobra.Command{
		Use: "ls", Aliases: []string{"list"}, Short: "List accounts",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl, err := cf.resolve(cmd, g)
			if err != nil {
				return err
			}
			resp, err := cl.ListAccountsWithResponse(cmd.Context())
			if err != nil {
				return err
			}
			return cf.respond(cmd.OutOrStdout(), resp.StatusCode(), resp.Body, 200, func(w io.Writer) error {
				return renderAccounts(w, *resp.JSON200)
			})
		},
	}

	test := &cobra.Command{
		Use: "test <id|name>", Short: "Verify an account against its provider", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := cf.resolve(cmd, g)
			if err != nil {
				return err
			}
			resp, err := cl.TestAccountWithResponse(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return cf.respond(cmd.OutOrStdout(), resp.StatusCode(), resp.Body, 200, func(w io.Writer) error {
				u := resp.JSON200
				_, err := fmt.Fprintf(w, "ok: %s premium=%v plan=%s\n", deref(u.Username), u.Premium, deref(u.Plan))
				return err
			})
		},
	}

	var force bool
	rm := &cobra.Command{
		Use: "rm <id|name>", Aliases: []string{"delete", "remove"}, Short: "Delete an account", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := cf.resolve(cmd, g)
			if err != nil {
				return err
			}
			resp, err := cl.DeleteAccountWithResponse(cmd.Context(), args[0], &apiclient.DeleteAccountParams{Force: &force})
			if err != nil {
				return err
			}
			return cf.respond(cmd.OutOrStdout(), resp.StatusCode(), resp.Body, 204, func(w io.Writer) error {
				_, err := fmt.Fprintln(w, "deleted", args[0])
				return err
			})
		},
	}
	rm.Flags().BoolVar(&force, "force", false, "also delete the account's torrents (locally)")

	var newName, newKey string
	var enable, disable, makeDefault bool
	set := &cobra.Command{
		Use: "set <id|name>", Short: "Update an account", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := cf.resolve(cmd, g)
			if err != nil {
				return err
			}
			body := apiclient.UpdateAccountJSONRequestBody{}
			if newName != "" {
				body.Name = &newName
			}
			if newKey != "" {
				body.Credentials = &apiclient.Credentials{ApiKey: &newKey}
			}
			if enable {
				body.Enabled = ptr(true)
			}
			if disable {
				body.Enabled = ptr(false)
			}
			if makeDefault {
				body.SetDefault = &makeDefault
			}
			resp, err := cl.UpdateAccountWithResponse(cmd.Context(), args[0], body)
			if err != nil {
				return err
			}
			return cf.respond(cmd.OutOrStdout(), resp.StatusCode(), resp.Body, 200, func(w io.Writer) error {
				return renderAccounts(w, []apiclient.Account{*resp.JSON200})
			})
		},
	}
	set.Flags().StringVar(&newName, "name", "", "rename")
	set.Flags().StringVar(&newKey, "key", "", "replace the provider API key")
	set.Flags().BoolVar(&enable, "enable", false, "enable the account")
	set.Flags().BoolVar(&disable, "disable", false, "disable the account")
	set.Flags().BoolVar(&makeDefault, "default", false, "make it the default account")

	cmd.AddCommand(add, ls, test, rm, set)
	return cmd
}

func renderAccounts(w io.Writer, accs []apiclient.Account) error {
	rows := make([][]string, 0, len(accs))
	for _, a := range accs {
		def, en := "", "yes"
		if a.IsDefault {
			def = "*"
		}
		if !a.Enabled {
			en = "no"
		}
		user := ""
		if a.User != nil {
			user = deref(a.User.Username)
			if a.User.Premium {
				user += " (premium)"
			}
		}
		rows = append(rows, []string{a.Id, a.Name, string(a.Kind), def, en, user})
	}
	return table(w, []string{"ID", "NAME", "KIND", "DEFAULT", "ENABLED", "USER"}, rows)
}
