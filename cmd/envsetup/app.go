package main

import (
	"io"

	"github.com/jigneshkhatri/envsetup/internal/registry"
)

// App holds the state shared across every command: the provider registry,
// I/O streams (overridable in tests), and the exit code a command can
// request beyond the default success (0) / error (1) split.
type App struct {
	Registry *registry.Registry
	Out      io.Writer
	Err      io.Writer
	In       io.Reader
	ExitCode int
}
