package flatpak

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// commandRunner abstracts running an external command, so tests can inject
// fixture output instead of invoking the real flatpak binary.
type commandRunner func(ctx context.Context, name string, args ...string) (stdout string, err error)

// execCommand is the real commandRunner used outside tests.
func execCommand(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}

	return stdout.String(), nil
}

// splitLines splits flatpak's newline-separated list output, dropping
// blank lines.
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
