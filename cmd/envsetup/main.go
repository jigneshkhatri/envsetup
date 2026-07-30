package main

import (
	"fmt"
	"os"

	"github.com/jigneshkhatri/envsetup/internal/providers/dotfiles"
	"github.com/jigneshkhatri/envsetup/internal/providers/gitrepos"
	"github.com/jigneshkhatri/envsetup/internal/providers/packages"
	"github.com/jigneshkhatri/envsetup/internal/registry"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	app := &App{
		Registry: registry.New(),
		Out:      os.Stdout,
		Err:      os.Stderr,
		In:       os.Stdin,
	}
	app.Registry.Register(packages.New())
	app.Registry.Register(dotfiles.New())
	app.Registry.Register(gitrepos.New())

	root := newRootCmd(app)
	root.SetOut(app.Out)
	root.SetErr(app.Err)

	if err := root.Execute(); err != nil {
		fmt.Fprintln(app.Err, "Error:", err)
		os.Exit(1)
	}
	os.Exit(app.ExitCode)
}
