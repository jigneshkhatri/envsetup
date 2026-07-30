package main

import (
	"os"

	"github.com/spf13/cobra"
)

func newRootCmd(app *App) *cobra.Command {
	root := &cobra.Command{
		Use:   "envsetup",
		Short: "Reproduce a Linux workstation from its exported state",
		Long: "EnvSetup captures the complete state of a Linux workstation and\n" +
			"recreates that same state on another machine safely, predictably,\n" +
			"and repeatedly.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().String("project", "", "project directory (default: $ENVSETUP_PROJECT, or the current directory)")
	root.PersistentFlags().BoolP("verbose", "v", false, "verbose output")

	root.AddCommand(newVersionCmd())
	root.AddCommand(newInitCmd(app))
	root.AddCommand(newScanCmd(app))
	root.AddCommand(newExportCmd(app))
	root.AddCommand(newPlanCmd(app))
	root.AddCommand(newApplyCmd(app))
	root.AddCommand(newValidateCmd(app))
	root.AddCommand(newDoctorCmd(app))

	return root
}

// resolveProjectDir determines the project directory for a command: the
// --project flag wins, then $ENVSETUP_PROJECT, then the current directory.
func resolveProjectDir(cmd *cobra.Command) string {
	if v, _ := cmd.Flags().GetString("project"); v != "" {
		return v
	}
	if v := os.Getenv("ENVSETUP_PROJECT"); v != "" {
		return v
	}
	return "."
}
