// Package recipe implements the "recipes" resource type: the vision doc's
// escape hatch for anything that can't be discovered or modeled by a
// dedicated provider. Recipes are never discovered -- they're declared
// directly in the project by hand, as a name plus an apply command and an
// idempotency check command.
package recipe

import (
	"context"
	"fmt"
	"io"

	"github.com/jigneshkhatri/envsetup/internal/core"
)

// Provider runs user-declared recipes. It is a core.UserDeclaredProvider:
// `envsetup export` skips it entirely, since Discover has nothing to find
// and must never overwrite hand-authored recipes.yaml entries.
type Provider struct {
	out    io.Writer
	run    commandRunner
	stream streamRunner
}

// New returns a Provider that streams recipe apply output to out (normally
// the CLI's stdout) and shells out to the real sh binary.
func New(out io.Writer) *Provider {
	return &Provider{out: out, run: execCheck, stream: execApply}
}

// newWithRunners is used by tests to inject fixture check/apply behavior.
func newWithRunners(out io.Writer, run commandRunner, stream streamRunner) *Provider {
	return &Provider{out: out, run: run, stream: stream}
}

func (p *Provider) Type() string { return "recipes" }

// UserDeclared implements core.UserDeclaredProvider.
func (p *Provider) UserDeclared() bool { return true }

// Discover never finds anything -- recipes exist only as declared intent.
func (p *Provider) Discover(ctx context.Context, sys core.SystemContext) ([]core.Resource, error) {
	return nil, nil
}

// Export is never called by the engine (UserDeclared skips it) but is
// implemented harmlessly to satisfy core.Provider.
func (p *Provider) Export(ctx context.Context, projectDir string, resources []core.Resource) ([]core.ProjectResource, error) {
	return nil, nil
}

// Plan runs each desired recipe's check command to decide whether it's
// already satisfied. current is ignored -- Discover never finds anything,
// so there is no "current" resource list to diff against; the check
// command result *is* the current state.
func (p *Provider) Plan(ctx context.Context, desired []core.ProjectResource, current []core.Resource) ([]core.Action, error) {
	var actions []core.Action

	for _, d := range desired {
		check, _ := d.Attributes["check"].(string)
		if check == "" {
			return nil, fmt.Errorf("recipes: %q has no check command (required for idempotency)", d.ID)
		}

		if p.run(ctx, check) {
			continue // already satisfied
		}

		apply, _ := d.Attributes["apply"].(string)
		actions = append(actions, core.Action{
			ResourceType: p.Type(), ResourceID: d.ID, Kind: core.ActionCreate,
			Description: fmt.Sprintf("run recipe %q", d.ID),
			Attributes:  map[string]any{"apply": apply},
		})
	}

	return actions, nil
}

// Apply streams the recipe's apply command output live to p.out as it
// runs, rather than capturing and printing on error -- recipes are the
// least "safe by construction" part of EnvSetup, so visibility matters most
// here.
func (p *Provider) Apply(ctx context.Context, projectDir string, action core.Action) error {
	apply, _ := action.Attributes["apply"].(string)
	if apply == "" {
		return fmt.Errorf("recipes: %q has no apply command", action.ResourceID)
	}

	fmt.Fprintf(p.out, "--- recipe %q ---\n", action.ResourceID)
	if err := p.stream(ctx, p.out, apply); err != nil {
		return fmt.Errorf("recipes: running %q: %w", action.ResourceID, err)
	}
	return nil
}

// Validate re-runs each desired recipe's check command only.
func (p *Provider) Validate(ctx context.Context, desired []core.ProjectResource) ([]core.ValidationResult, error) {
	results := make([]core.ValidationResult, 0, len(desired))

	for _, d := range desired {
		check, _ := d.Attributes["check"].(string)
		if check == "" {
			return nil, fmt.Errorf("recipes: %q has no check command (required for idempotency)", d.ID)
		}

		satisfied := p.run(ctx, check)
		result := core.ValidationResult{ResourceType: p.Type(), ResourceID: d.ID, Drifted: !satisfied}
		if !satisfied {
			result.Detail = "check command not satisfied"
		}
		results = append(results, result)
	}

	return results, nil
}
