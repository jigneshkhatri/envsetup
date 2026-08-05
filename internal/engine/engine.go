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
//
// A single provider failing to Discover does not stop the others: that
// provider is skipped, its error recorded, and every other provider is
// still scanned. Callers get back both the results that could be gathered
// and an aggregate error describing what couldn't.
func (e *Engine) Scan(ctx context.Context) (map[string][]core.Resource, error) {
	found := make(map[string][]core.Resource)
	var errs []error

	for _, p := range e.Registry.All() {
		resources, err := p.Discover(ctx, e.Sys)
		if err != nil {
			errs = append(errs, fmt.Errorf("engine: discovering %s: %w", p.Type(), err))
			continue
		}
		found[p.Type()] = resources
	}
	return found, errors.Join(errs...)
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
//
// A single provider failing to Discover or Export does not stop the
// others: that provider is skipped, its error recorded, and every other
// provider is still exported. Callers get back both the results that could
// be gathered and an aggregate error describing what couldn't.
func (e *Engine) Export(ctx context.Context) ([]ExportResult, error) {
	var results []ExportResult
	var errs []error

	for _, p := range e.Registry.All() {
		if ud, ok := p.(core.UserDeclaredProvider); ok && ud.UserDeclared() {
			continue
		}

		discovered, err := p.Discover(ctx, e.Sys)
		if err != nil {
			errs = append(errs, fmt.Errorf("engine: discovering %s: %w", p.Type(), err))
			continue
		}

		exported, err := p.Export(ctx, e.Project.Dir, discovered)
		if err != nil {
			errs = append(errs, fmt.Errorf("engine: exporting %s: %w", p.Type(), err))
			continue
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

	return results, errors.Join(errs...)
}

// Plan diffs every provider's desired resources (from the project) against
// current resources (from a fresh Discover) and returns the merged,
// deterministically ordered list of non-noop actions needed to reconcile
// them. Never modifies the system.
//
// A single provider failing to Discover or Plan (e.g. a transient pacman
// error, or a permissions issue reading one config path) does not stop the
// others: that provider is skipped, its error recorded, and every other
// provider is still planned. Callers get back both the actions that could
// be computed and an aggregate error describing what couldn't -- never a
// forced choice between "nothing" and "everything but the one that broke."
func (e *Engine) Plan(ctx context.Context) ([]core.Action, error) {
	var actions []core.Action
	var errs []error

	for _, p := range e.Registry.All() {
		current, err := p.Discover(ctx, e.Sys)
		if err != nil {
			errs = append(errs, fmt.Errorf("engine: discovering %s: %w", p.Type(), err))
			continue
		}

		desired := e.Project.ResourcesFor(p.Type())

		typeActions, err := p.Plan(ctx, desired, current)
		if err != nil {
			errs = append(errs, fmt.Errorf("engine: planning %s: %w", p.Type(), err))
			continue
		}

		for _, a := range typeActions {
			if a.Kind != core.ActionNoop {
				actions = append(actions, a)
			}
		}
	}

	return actions, errors.Join(errs...)
}

// ApplyOptions configures Apply.
type ApplyOptions struct {
	// Only restricts apply to these resource types. Empty means all types.
	Only []string
	// DryRun computes the result but executes nothing.
	DryRun bool
	// AllowUpdate permits executing Update actions -- e.g. overwriting a
	// drifted dotfile's content, or checking out a different git ref.
	// Without it, Update actions are reported in Skipped but never
	// executed: apply never silently overrides configuration already on
	// the host.
	AllowUpdate bool
	// AllowRemove permits executing Delete actions -- e.g. uninstalling a
	// package, or disabling a service. Without it, Delete actions are
	// reported in Skipped but never executed: apply never silently
	// removes configuration already on the host.
	AllowRemove bool
	// OnActionStart, if set, is called immediately before Apply executes
	// each action against the live system -- not called under DryRun.
	// This is what turns a long or many-action apply from a single silent
	// block into continuous progress output.
	OnActionStart func(core.Action)
	// OnActionDone, if set, is called immediately after each action
	// finishes, err nil on success. Apply keeps going past a failure (see
	// Apply's doc comment), so this is what makes a failure visible in
	// real time rather than only once the whole batch completes.
	OnActionDone func(core.Action, error)
}

// ApplyResult is what Apply executed (or would execute, under DryRun) and
// what it deliberately left alone.
type ApplyResult struct {
	// Applied are the actions that were executed (or would be, under
	// DryRun): every Create action, plus Update/Delete actions only if
	// explicitly allowed.
	Applied []core.Action
	// Skipped are Update/Delete actions that were part of the plan but
	// left untouched because AllowUpdate/AllowRemove wasn't set.
	Skipped []core.Action
}

// Apply always recomputes the plan first -- there is no path to mutate the
// system without a fresh diff. By default only Create actions run (filling
// in resources that are declared but missing); Update and Delete actions
// require their respective opt-in flag, so apply never overrides or
// removes configuration already on the host unless explicitly told to.
// Among the actions that do run, Apply continues past a failure so one
// resource's failure does not block unrelated resources, returning every
// action it attempted alongside an aggregate error, if any. This extends to
// planning itself: if one provider fails to Discover or Plan, Apply still
// executes whatever actions the rest of the plan produced, folding the
// planning failure into the same aggregate error rather than aborting
// before anything runs.
func (e *Engine) Apply(ctx context.Context, opts ApplyOptions) (*ApplyResult, error) {
	actions, planErr := e.Plan(ctx)

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

	result := &ApplyResult{}
	for _, a := range actions {
		switch a.Kind {
		case core.ActionUpdate:
			if !opts.AllowUpdate {
				result.Skipped = append(result.Skipped, a)
				continue
			}
		case core.ActionDelete:
			if !opts.AllowRemove {
				result.Skipped = append(result.Skipped, a)
				continue
			}
		}
		result.Applied = append(result.Applied, a)
	}

	if opts.DryRun {
		return result, planErr
	}

	var errs []error
	if planErr != nil {
		errs = append(errs, planErr)
	}
	for _, a := range result.Applied {
		if opts.OnActionStart != nil {
			opts.OnActionStart(a)
		}

		p, ok := e.Registry.Get(a.ResourceType)
		if !ok {
			err := fmt.Errorf("engine: no provider registered for type %q", a.ResourceType)
			errs = append(errs, err)
			if opts.OnActionDone != nil {
				opts.OnActionDone(a, err)
			}
			continue
		}

		applyErr := p.Apply(ctx, e.Project.Dir, a)
		if applyErr != nil {
			applyErr = fmt.Errorf("engine: applying %s %q: %w", a.ResourceType, a.ResourceID, applyErr)
			errs = append(errs, applyErr)
		}
		if opts.OnActionDone != nil {
			opts.OnActionDone(a, applyErr)
		}
	}

	return result, errors.Join(errs...)
}

// Validate reports drift between every provider's desired resources and
// live system state, without modifying anything.
//
// A single provider failing to Validate does not stop the others: that
// provider is skipped, its error recorded, and every other provider is
// still validated. Callers get back both the results that could be
// gathered and an aggregate error describing what couldn't.
func (e *Engine) Validate(ctx context.Context) ([]core.ValidationResult, error) {
	var results []core.ValidationResult
	var errs []error

	for _, p := range e.Registry.All() {
		desired := e.Project.ResourcesFor(p.Type())

		typeResults, err := p.Validate(ctx, desired)
		if err != nil {
			errs = append(errs, fmt.Errorf("engine: validating %s: %w", p.Type(), err))
			continue
		}

		results = append(results, typeResults...)
	}

	return results, errors.Join(errs...)
}

// Doctor runs cross-provider health diagnostics: generic project schema
// checks (blank or duplicate resource IDs), plus each provider's own
// Doctor checks (for providers that implement core.DoctorProvider), never
// modifying anything.
//
// A single provider failing its Doctor check does not stop the others:
// that provider is skipped, its error recorded, and every other provider's
// diagnostics still run. Callers get back both the diagnoses that could be
// gathered and an aggregate error describing what couldn't.
func (e *Engine) Doctor(ctx context.Context) ([]core.Diagnosis, error) {
	var diagnoses []core.Diagnosis

	for _, typ := range e.Project.Types() {
		seen := make(map[string]bool)
		for _, r := range e.Project.ResourcesFor(typ) {
			if r.ID == "" {
				diagnoses = append(diagnoses, core.Diagnosis{ResourceType: typ, Message: "a resource has an empty id"})
				continue
			}
			if seen[r.ID] {
				diagnoses = append(diagnoses, core.Diagnosis{ResourceType: typ, ResourceID: r.ID, Message: "duplicate id declared more than once"})
				continue
			}
			seen[r.ID] = true
		}
	}

	var errs []error
	for _, p := range e.Registry.All() {
		dp, ok := p.(core.DoctorProvider)
		if !ok {
			continue
		}

		desired := e.Project.ResourcesFor(p.Type())
		if len(desired) == 0 {
			continue
		}

		found, err := dp.Doctor(ctx, e.Project.Dir, desired)
		if err != nil {
			errs = append(errs, fmt.Errorf("engine: diagnosing %s: %w", p.Type(), err))
			continue
		}
		diagnoses = append(diagnoses, found...)
	}

	return diagnoses, errors.Join(errs...)
}
