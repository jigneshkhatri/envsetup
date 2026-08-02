package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/jigneshkhatri/envsetup/internal/core"
	"github.com/jigneshkhatri/envsetup/internal/engine"
	"github.com/jigneshkhatri/envsetup/internal/ui"
)

func newApplyCmd(app *App) *cobra.Command {
	var yes, dryRun, allowUpdate, allowRemove bool
	var only []string

	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Reconcile the workstation with the project",
		Long: "By default, apply only fills in resources that are declared but\n" +
			"missing -- it never overrides or removes configuration already on\n" +
			"the host. Pass --allow-update to let it overwrite drifted resources\n" +
			"(e.g. a dotfile whose content changed), and --allow-remove to let it\n" +
			"remove resources that exist but aren't declared (e.g. uninstall a\n" +
			"package, disable a service).",
		RunE: func(cmd *cobra.Command, args []string) error {
			proj, _, err := loadProjectOrHint(cmd)
			if err != nil {
				return err
			}

			e := engine.New(app.Registry, proj, systemContext())
			succeeded := 0
			opts := engine.ApplyOptions{
				Only: only, AllowUpdate: allowUpdate, AllowRemove: allowRemove,
				OnActionStart: func(a core.Action) { ui.PrintApplyStart(app.Out, a) },
				OnActionDone: func(a core.Action, err error) {
					ui.PrintApplyResult(app.Out, a, err)
					if err == nil {
						succeeded++
					}
				},
			}

			// Apply always re-diffs before doing anything -- there is no
			// path to mutate the system without a fresh plan.
			previewOpts := opts
			previewOpts.DryRun = true
			preview, err := e.Apply(context.Background(), previewOpts)
			if err != nil {
				return err
			}

			ui.PrintPlan(app.Out, append(append([]core.Action{}, preview.Applied...), preview.Skipped...))

			if len(preview.Skipped) > 0 {
				fmt.Fprintf(app.Out, "\n%d action(s) skipped by default (pass --allow-update and/or --allow-remove to include them).\n", len(preview.Skipped))
			}

			if len(preview.Applied) == 0 {
				fmt.Fprintln(app.Out, "\nNothing to apply.")
				return nil
			}
			if dryRun {
				return nil
			}

			if !yes {
				ok, err := ui.Confirm(app.In, app.Out, fmt.Sprintf("\nApply these %d action(s)? [y/N] ", len(preview.Applied)), false)
				if err != nil {
					return err
				}
				if !ok {
					fmt.Fprintln(app.Out, "Apply cancelled. No changes made.")
					return nil
				}
			}

			result, applyErr := e.Apply(context.Background(), opts)
			if applyErr != nil {
				fmt.Fprintf(app.Out, "\n%d of %d action(s) applied successfully.\n", succeeded, len(result.Applied))
			} else {
				fmt.Fprintf(app.Out, "\nApplied %d action(s).\n", len(result.Applied))
			}
			return applyErr
		},
	}

	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "apply without interactive confirmation")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show the plan without applying it")
	cmd.Flags().BoolVar(&allowUpdate, "allow-update", false, "allow overwriting resources that already exist but have drifted")
	cmd.Flags().BoolVar(&allowRemove, "allow-remove", false, "allow removing resources that exist but aren't declared in the project")
	cmd.Flags().StringSliceVar(&only, "only", nil, "restrict apply to these resource types")
	return cmd
}
