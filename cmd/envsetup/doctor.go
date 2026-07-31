package main

import (
	"context"

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
			diagnoses, err := e.Doctor(context.Background())
			if err != nil {
				return err
			}

			ui.PrintDoctor(app.Out, diagnoses)
			if len(diagnoses) > 0 {
				app.ExitCode = 2
			}
			return nil
		},
	}
}
