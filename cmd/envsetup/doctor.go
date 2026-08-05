package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/jigneshkhatri/envsetup/internal/engine"
	"github.com/jigneshkhatri/envsetup/internal/ui"
)

func newDoctorCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose the project and workstation for common problems",
		Long: "Doctor never modifies anything. Exit code 0 means no issues were\n" +
			"found, 2 means issues were found, 1 means an error occurred.",
		RunE: func(cmd *cobra.Command, args []string) error {
			proj, _, err := loadProjectOrHint(cmd)
			if err != nil {
				return err
			}

			e := engine.New(app.Registry, proj, systemContext())
			// A provider that fails its Doctor check doesn't block the
			// rest -- it's reported here, and every other provider's
			// diagnoses are still shown.
			diagnoses, doctorErr := e.Doctor(context.Background())
			if doctorErr != nil {
				fmt.Fprintf(app.Out, "warning: %v\n\n", doctorErr)
			}

			ui.PrintDoctor(app.Out, diagnoses)
			if len(diagnoses) > 0 {
				app.ExitCode = 2
			}
			return doctorErr
		},
	}
}
