// Package pacman holds small helpers around the pacman CLI that are
// shared by multiple providers, so each one doesn't reimplement the same
// check.
package pacman

import "context"

// Owns reports whether path is owned by an installed package. `pacman -Qo`
// exits non-zero with "No package owns <path>" when nothing does -- that
// failure is the signal itself, not an error condition, so Owns never
// returns a non-nil error itself.
//
// run's parameter type is left unnamed (rather than a named type in this
// package) so that any provider's own commandRunner-shaped function value
// is directly assignable here without an adapter.
func Owns(ctx context.Context, run func(ctx context.Context, name string, args ...string) (string, error), path string) bool {
	_, err := run(ctx, "pacman", "-Qo", path)
	return err == nil
}
