package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newDoctorCmd is intentionally minimal for now: real cross-provider
// diagnostics (broken symlinks, orphaned resources, project schema
// validation, ...) are Phase 9 work, once there are real providers to
// diagnose. This wires the command and proves it behaves sensibly with an
// empty registry.
func newDoctorCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose the project and workstation for common problems",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, absDir, err := loadProjectOrHint(cmd)
			if err != nil {
				fmt.Fprintln(app.Out, err)
				return nil
			}

			if len(app.Registry.All()) == 0 {
				fmt.Fprintln(app.Out, "No providers registered yet -- nothing to diagnose.")
				return nil
			}

			fmt.Fprintf(app.Out, "No issues found in %s.\n", absDir)
			return nil
		},
	}
}
