package main

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/jigneshkhatri/envsetup/internal/engine"
	"github.com/jigneshkhatri/envsetup/internal/ui"
)

func newValidateCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Check whether the workstation still matches the project (drift detection)",
		Long: "Validate never modifies the system. Exit code 0 means no drift was\n" +
			"found, 2 means drift was found, 1 means an error occurred.",
		RunE: func(cmd *cobra.Command, args []string) error {
			proj, _, err := loadProjectOrHint(cmd)
			if err != nil {
				return err
			}

			e := engine.New(app.Registry, proj, systemContext())
			results, err := e.Validate(context.Background())
			if err != nil {
				return err
			}

			ui.PrintValidation(app.Out, results)
			for _, r := range results {
				if r.Drifted {
					app.ExitCode = 2
					break
				}
			}
			return nil
		},
	}
}
