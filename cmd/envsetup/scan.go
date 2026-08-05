package main

import (
	"context"
	"fmt"

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
			// A provider that fails to discover doesn't block the rest --
			// it's reported here, and every other provider's results are
			// still shown.
			found, err := e.Scan(context.Background())
			if err != nil {
				fmt.Fprintf(app.Out, "warning: %v\n\n", err)
			}

			ui.PrintScan(app.Out, found, verbose)
			return err
		},
	}
}
