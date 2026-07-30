package gitrepos

import (
	"context"
	"strings"
	"testing"
)

func TestExecCommandReturnsStdout(t *testing.T) {
	out, err := execCommand(context.Background(), "sh", "-c", "echo hello")
	if err != nil {
		t.Fatalf("execCommand: %v", err)
	}
	if strings.TrimSpace(out) != "hello" {
		t.Errorf("got %q, want hello", out)
	}
}

func TestExecCommandPropagatesErrors(t *testing.T) {
	_, err := execCommand(context.Background(), "sh", "-c", "echo boom >&2; exit 1")
	if err == nil {
		t.Fatal("execCommand: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error should include stderr output, got: %v", err)
	}
}
