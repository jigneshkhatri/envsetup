package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/jigneshkhatri/envsetup/internal/engine"
	"github.com/jigneshkhatri/envsetup/internal/ui"
)

func newApplyCmd(app *App) *cobra.Command {
	var yes, dryRun bool
	var only []string

	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Reconcile the workstation with the project",
		RunE: func(cmd *cobra.Command, args []string) error {
			proj, _, err := loadProjectOrHint(cmd)
			if err != nil {
				return err
			}

			e := engine.New(app.Registry, proj, systemContext())

			// Apply always re-diffs before doing anything -- there is no
			// path to mutate the system without a fresh plan.
			preview, err := e.Apply(context.Background(), engine.ApplyOptions{Only: only, DryRun: true})
			if err != nil {
				return err
			}

			ui.PrintPlan(app.Out, preview)

			if len(preview) == 0 || dryRun {
				return nil
			}

			if !yes {
				ok, err := ui.Confirm(app.In, app.Out, fmt.Sprintf("\nApply these %d action(s)? [y/N] ", len(preview)), false)
				if err != nil {
					return err
				}
				if !ok {
					fmt.Fprintln(app.Out, "Apply cancelled. No changes made.")
					return nil
				}
			}

			applied, applyErr := e.Apply(context.Background(), engine.ApplyOptions{Only: only})
			fmt.Fprintf(app.Out, "\nApplied %d action(s).\n", len(applied))
			return applyErr
		},
	}

	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "apply without interactive confirmation")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show the plan without applying it")
	cmd.Flags().StringSliceVar(&only, "only", nil, "restrict apply to these resource types")
	return cmd
}
