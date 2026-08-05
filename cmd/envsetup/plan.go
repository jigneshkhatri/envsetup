package main

import (
	"context"
	"fmt"

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
			// A provider that fails to plan doesn't block the rest -- it's
			// reported here, and every other provider's plan is still shown.
			actions, planErr := e.Plan(context.Background())
			if planErr != nil {
				fmt.Fprintf(app.Out, "warning: %v\n\n", planErr)
			}

			ui.PrintPlan(app.Out, actions)
			if len(actions) > 0 {
				app.ExitCode = 2
			}
			return planErr
		},
	}
}
