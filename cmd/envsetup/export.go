package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/jigneshkhatri/envsetup/internal/core"
	"github.com/jigneshkhatri/envsetup/internal/engine"
	"github.com/jigneshkhatri/envsetup/internal/project"
	"github.com/jigneshkhatri/envsetup/internal/ui"
)

func newExportCmd(app *App) *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:   "export [path]",
		Short: "Write an EnvSetup project from the workstation's discovered state",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := resolveProjectDir(cmd)
			if len(args) == 1 {
				dir = args[0]
			}

			absDir, err := filepath.Abs(dir)
			if err != nil {
				return fmt.Errorf("resolving %s: %w", dir, err)
			}

			proj, err := project.Load(absDir)
			if err != nil {
				proj = project.New(absDir, filepath.Base(absDir))
			}

			e := engine.New(app.Registry, proj, systemContext())
			// A provider that fails to discover/export doesn't block the
			// rest -- it's reported here, and every other provider's
			// results are still exported.
			results, exportErr := e.Export(context.Background())
			if exportErr != nil {
				fmt.Fprintf(app.Out, "warning: %v\n\n", exportErr)
			}

			fmt.Fprintln(app.Out)
			for _, r := range results {
				exported := r.Exported
				if len(r.NeedsReview) > 0 && !yes {
					var err error
					exported, err = reviewLowConfidence(app, r)
					if err != nil {
						return err
					}
				}
				proj.SetResourcesFor(r.Type, exported)
				fmt.Fprintf(app.Out, "%s: %d exported\n", r.Type, len(exported))
			}

			if err := proj.Save(); err != nil {
				return err
			}

			fmt.Fprintf(app.Out, "\nProject written to %s\n", absDir)
			return exportErr
		},
	}

	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "include low-confidence resources without interactive review")
	return cmd
}

// reviewLowConfidence prompts the user for each discovered resource whose
// confidence was too low to include automatically, dropping declined
// resources from the exported set. Per the vision doc, EnvSetup never
// guesses -- low-confidence resources are surfaced, not silently included.
func reviewLowConfidence(app *App, r engine.ExportResult) ([]core.ProjectResource, error) {
	excluded := make(map[string]bool)
	for _, res := range r.NeedsReview {
		detail := fmt.Sprintf("confidence: %s", res.Confidence)
		if fc, ok := res.Attributes["file_count"]; ok {
			detail = fmt.Sprintf("%s, %v files", detail, fc)
		}
		prompt := fmt.Sprintf("Include %s %q (%s)? [y/N] ", r.Type, res.ID, detail)
		ok, err := ui.Confirm(app.In, app.Out, prompt, false)
		if err != nil {
			return nil, err
		}
		if !ok {
			excluded[res.ID] = true
		}
	}

	if len(excluded) == 0 {
		return r.Exported, nil
	}

	kept := make([]core.ProjectResource, 0, len(r.Exported))
	for _, pr := range r.Exported {
		if !excluded[pr.ID] {
			kept = append(kept, pr)
		}
	}
	return kept, nil
}
