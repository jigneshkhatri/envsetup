package recipe

import (
	"bytes"
	"context"
	"io"
	"os/exec"
)

// commandRunner runs a shell check command and reports only whether it
// succeeded, so Plan/Validate can decide satisfied vs. pending. Output is
// captured, not shown -- checks are expected to be quick and silent.
type commandRunner func(ctx context.Context, script string) (satisfied bool)

// execCheck is the real commandRunner used outside tests.
func execCheck(ctx context.Context, script string) bool {
	cmd := exec.CommandContext(ctx, "sh", "-c", script)
	var discard bytes.Buffer
	cmd.Stdout = &discard
	cmd.Stderr = &discard
	return cmd.Run() == nil
}

// streamRunner runs a recipe's apply script, streaming its combined
// stdout/stderr live to out as it runs -- recipes are the least
// "safe by construction" part of EnvSetup, so visibility matters most here.
type streamRunner func(ctx context.Context, out io.Writer, script string) error

// execApply is the real streamRunner used outside tests.
func execApply(ctx context.Context, out io.Writer, script string) error {
	cmd := exec.CommandContext(ctx, "sh", "-c", script)
	cmd.Stdout = out
	cmd.Stderr = out
	return cmd.Run()
}
