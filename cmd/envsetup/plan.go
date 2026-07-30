package main

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/jigneshkhatri/envsetup/internal/engine"
	"github.com/jigneshkhatri/envsetup/internal/ui"
)

func newPlanCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "plan",
		Short: "Show what would change to reconcile the workstation with the project",
		Long: "Plan never modifies the system. Exit code 0 means no changes are\n" +
			"needed, 2 means changes are pending, 1 means an error occurred.",
		RunE: func(cmd *cobra.Command, args []string) error {
			proj, _, err := loadProjectOrHint(cmd)
			if err != nil {
				return err
			}

			e := engine.New(app.Registry, proj, systemContext())
			actions, err := e.Plan(context.Background())
			if err != nil {
				return err
			}

			ui.PrintPlan(app.Out, actions)
			if len(actions) > 0 {
				app.ExitCode = 2
			}
			return nil
		},
	}
}
