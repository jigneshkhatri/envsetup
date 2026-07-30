package packages

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// commandRunner abstracts running an external command, so tests can inject
// fixture output instead of invoking real pacman/AUR-helper binaries.
type commandRunner func(ctx context.Context, name string, args ...string) (stdout string, err error)

// execCommand is the real commandRunner used outside tests.
func execCommand(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// pacman query filters (e.g. `-Qm` with no foreign packages
		// installed) exit 1 with no output when they simply match nothing
		// -- that's not a real error, just an empty result set.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 && stdout.Len() == 0 && stderr.Len() == 0 {
			return "", nil
		}
		return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}

	return stdout.String(), nil
}

// detectAURHelper returns the name of the first supported AUR helper found
// in PATH, or "" if none is installed.
func detectAURHelper() string {
	for _, name := range []string{"yay", "paru"} {
		if _, err := exec.LookPath(name); err == nil {
			return name
		}
	}
	return ""
}

// splitLines splits pacman's newline-separated list output, dropping the
// trailing empty entry and any blank lines.
func splitLines(s string) []string {
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
