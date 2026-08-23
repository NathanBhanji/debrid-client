package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"

	"github.com/NathanBhanji/debrid-client/internal/config"
)

// globalFlags holds flag values shared by all commands.
type globalFlags struct {
	configFile string
}

// loadConfig resolves configuration from file/env/flags for the given command.
func (g *globalFlags) loadConfig(cmd *cobra.Command) (config.Config, error) {
	return config.Load(config.Options{
		File:         g.configFile,
		FileExplicit: g.configFile != "",
		Flags:        cmd.Flags(),
	})
}

func newConfigCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect and initialise configuration",
	}

	initCmd := &cobra.Command{
		Use:   "init",
		Short: "Write a default config file (fails if it already exists)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := g.configFile
			if path == "" {
				path = config.DefaultConfigPath()
			}
			if err := config.WriteDefaultFile(path); err != nil {
				return err
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "wrote", path)
			return err
		},
	}

	showCmd := &cobra.Command{
		Use:   "show",
		Short: "Print the effective configuration (secrets redacted)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := g.loadConfig(cmd)
			if err != nil {
				return err
			}
			out, err := yaml.Marshal(cfg.Redacted())
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(out)
			return err
		},
	}
	config.BindFlags(showCmd.Flags())

	pathCmd := &cobra.Command{
		Use:   "path",
		Short: "Print the config file path in use",
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := g.configFile
			if path == "" {
				path = config.DefaultConfigPath()
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), path)
			return err
		},
	}

	cmd.AddCommand(initCmd, showCmd, pathCmd)
	return cmd
}
