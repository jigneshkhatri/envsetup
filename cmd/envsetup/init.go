package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/jigneshkhatri/envsetup/internal/project"
)

func newInitCmd(app *App) *cobra.Command {
	var name string

	cmd := &cobra.Command{
		Use:   "init [path]",
		Short: "Scaffold a new, empty EnvSetup project",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}

			absDir, err := filepath.Abs(dir)
			if err != nil {
				return fmt.Errorf("resolving %s: %w", dir, err)
			}

			if _, err := os.Stat(filepath.Join(absDir, "envsetup.yaml")); err == nil {
				return fmt.Errorf("a project already exists at %s", absDir)
			}

			projectName := name
			if projectName == "" {
				projectName = filepath.Base(absDir)
			}

			proj := project.New(absDir, projectName)
			if err := proj.Save(); err != nil {
				return err
			}

			fmt.Fprintf(app.Out, "Initialized empty EnvSetup project %q in %s\n", projectName, absDir)
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "project name (default: directory name)")
	return cmd
}
