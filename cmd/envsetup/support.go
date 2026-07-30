package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/jigneshkhatri/envsetup/internal/core"
	"github.com/jigneshkhatri/envsetup/internal/project"
)

// systemContext builds the SystemContext passed to every provider's
// Discover call.
func systemContext() core.SystemContext {
	home, _ := os.UserHomeDir()
	return core.SystemContext{HomeDir: home}
}

// loadProjectOrHint resolves the project directory for cmd and loads it,
// returning a helpful error (pointing at `envsetup init`) if none exists.
func loadProjectOrHint(cmd *cobra.Command) (*project.Project, string, error) {
	dir := resolveProjectDir(cmd)

	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, "", fmt.Errorf("resolving %s: %w", dir, err)
	}

	proj, err := project.Load(absDir)
	if err != nil {
		return nil, "", fmt.Errorf("no EnvSetup project found in %s (run `envsetup init` first): %w", absDir, err)
	}

	return proj, absDir, nil
}
