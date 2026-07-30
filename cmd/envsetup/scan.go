package main

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/jigneshkhatri/envsetup/internal/engine"
	"github.com/jigneshkhatri/envsetup/internal/ui"
)

func newScanCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "scan",
		Short: "Discover supported resources on this workstation (read-only)",
		RunE: func(cmd *cobra.Command, args []string) error {
			verbose, _ := cmd.Flags().GetBool("verbose")

			e := engine.New(app.Registry, nil, systemContext())
			found, err := e.Scan(context.Background())
			if err != nil {
				return err
			}

			ui.PrintScan(app.Out, found, verbose)
			return nil
		},
	}
}
