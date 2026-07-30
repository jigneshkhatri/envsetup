package packages

import (
	"context"
	"strings"
	"testing"
)

// TestExecCommandTreatsEmptyExitOneAsEmptyResult locks in the pacman query
// quirk (e.g. `pacman -Qm` with nothing to list exits 1 with no output)
// without depending on pacman actually being installed.
func TestExecCommandTreatsEmptyExitOneAsEmptyResult(t *testing.T) {
	out, err := execCommand(context.Background(), "sh", "-c", "exit 1")
	if err != nil {
		t.Fatalf("execCommand: unexpected error for empty exit-1: %v", err)
	}
	if out != "" {
		t.Errorf("got output %q, want empty", out)
	}
}

func TestExecCommandPropagatesRealErrors(t *testing.T) {
	_, err := execCommand(context.Background(), "sh", "-c", "echo boom >&2; exit 1")
	if err == nil {
		t.Fatal("execCommand: expected error when stderr is non-empty, got nil")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error should include stderr output, got: %v", err)
	}
}

func TestExecCommandReturnsStdout(t *testing.T) {
	out, err := execCommand(context.Background(), "sh", "-c", "echo hello")
	if err != nil {
		t.Fatalf("execCommand: %v", err)
	}
	if strings.TrimSpace(out) != "hello" {
		t.Errorf("got %q, want hello", out)
	}
}
