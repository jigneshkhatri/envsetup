// Package engine drives the generic scan/export/plan/apply/validate
// lifecycle across every registered provider. It contains no resource-type
// -specific logic -- that belongs to individual providers (internal/providers/*)
// reached only through the registry.
package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/jigneshkhatri/envsetup/internal/core"
	"github.com/jigneshkhatri/envsetup/internal/project"
	"github.com/jigneshkhatri/envsetup/internal/registry"
)

// Engine ties a provider registry to a project and the live system.
type Engine struct {
	Registry *registry.Registry
	Project  *project.Project
	Sys      core.SystemContext
}

// New returns an Engine wired to reg, proj, and sys.
func New(reg *registry.Registry, proj *project.Project, sys core.SystemContext) *Engine {
	return &Engine{Registry: reg, Project: proj, Sys: sys}
}

// Scan runs Discover across every registered provider. Read-only -- never
// modifies the system or the project.
func (e *Engine) Scan(ctx context.Context) (map[string][]core.Resource, error) {
	found := make(map[string][]core.Resource)
	for _, p := range e.Registry.All() {
		resources, err := p.Discover(ctx, e.Sys)
		if err != nil {
			return nil, fmt.Errorf("engine: discovering %s: %w", p.Type(), err)
		}
		found[p.Type()] = resources
	}
	return found, nil
}

// ExportResult is one provider's Export output: the resources ready to be
// written into the project, and any discovered resources whose confidence
// was too low to include automatically.
type ExportResult struct {
	Type        string
	Exported    []core.ProjectResource
	NeedsReview []core.Resource
}

// Export runs Discover then Export across every registered provider. It
// does not write anything to disk or mutate the in-memory project --
// callers decide (after any interactive review of NeedsReview items)
// whether to call Project.SetResourcesFor and Project.Save.
func (e *Engine) Export(ctx context.Context) ([]ExportResult, error) {
	var results []ExportResult

	for _, p := range e.Registry.All() {
		discovered, err := p.Discover(ctx, e.Sys)
		if err != nil {
			return nil, fmt.Errorf("engine: discovering %s: %w", p.Type(), err)
		}

		exported, err := p.Export(ctx, discovered)
		if err != nil {
			return nil, fmt.Errorf("engine: exporting %s: %w", p.Type(), err)
		}

		var needsReview []core.Resource
		for _, r := range discovered {
			if r.Confidence < core.ConfidenceHigh {
				needsReview = append(needsReview, r)
			}
		}

		results = append(results, ExportResult{
			Type:        p.Type(),
			Exported:    exported,
			NeedsReview: needsReview,
		})
	}

	return results, nil
}

// Plan diffs every provider's desired resources (from the project) against
// current resources (from a fresh Discover) and returns the merged,
// deterministically ordered list of non-noop actions needed to reconcile
// them. Never modifies the system.
func (e *Engine) Plan(ctx context.Context) ([]core.Action, error) {
	var actions []core.Action

	for _, p := range e.Registry.All() {
		current, err := p.Discover(ctx, e.Sys)
		if err != nil {
			return nil, fmt.Errorf("engine: discovering %s: %w", p.Type(), err)
		}

		desired := e.Project.ResourcesFor(p.Type())

		typeActions, err := p.Plan(ctx, desired, current)
		if err != nil {
			return nil, fmt.Errorf("engine: planning %s: %w", p.Type(), err)
		}

		for _, a := range typeActions {
			if a.Kind != core.ActionNoop {
				actions = append(actions, a)
			}
		}
	}

	return actions, nil
}

// ApplyOptions configures Apply.
type ApplyOptions struct {
	// Only restricts apply to these resource types. Empty means all types.
	Only []string
	// DryRun computes the plan but executes nothing.
	DryRun bool
}

// Apply always recomputes the plan first -- there is no path to mutate the
// system without a fresh diff -- then executes each action in order. It
// continues past a failed action so one resource's failure does not block
// unrelated resources, returning every action it attempted alongside an
// aggregate error, if any.
func (e *Engine) Apply(ctx context.Context, opts ApplyOptions) ([]core.Action, error) {
	actions, err := e.Plan(ctx)
	if err != nil {
		return nil, err
	}

	if len(opts.Only) > 0 {
		allowed := make(map[string]bool, len(opts.Only))
		for _, t := range opts.Only {
			allowed[t] = true
		}

		var filtered []core.Action
		for _, a := range actions {
			if allowed[a.ResourceType] {
				filtered = append(filtered, a)
			}
		}
		actions = filtered
	}

	if opts.DryRun {
		return actions, nil
	}

	var errs []error
	for _, a := range actions {
		p, ok := e.Registry.Get(a.ResourceType)
		if !ok {
			errs = append(errs, fmt.Errorf("engine: no provider registered for type %q", a.ResourceType))
			continue
		}
		if err := p.Apply(ctx, a); err != nil {
			errs = append(errs, fmt.Errorf("engine: applying %s %q: %w", a.ResourceType, a.ResourceID, err))
		}
	}

	return actions, errors.Join(errs...)
}

// Validate reports drift between every provider's desired resources and
// live system state, without modifying anything.
func (e *Engine) Validate(ctx context.Context) ([]core.ValidationResult, error) {
	var results []core.ValidationResult

	for _, p := range e.Registry.All() {
		desired := e.Project.ResourcesFor(p.Type())

		typeResults, err := p.Validate(ctx, desired)
		if err != nil {
			return nil, fmt.Errorf("engine: validating %s: %w", p.Type(), err)
		}

		results = append(results, typeResults...)
	}

	return results, nil
}
