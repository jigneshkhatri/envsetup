# Contributing to EnvSetup

The core engine (`internal/core`, `internal/engine`, `internal/project`,
`internal/registry`) knows nothing about pacman, dotfiles, or systemd. All of
that lives in a **provider** — a small, self-contained package under
`internal/providers/` that teaches the engine how to handle one resource
type. Adding support for something new almost always means writing a new
provider, not touching the core.

## The `Provider` interface

Every provider implements `core.Provider` (`internal/core/provider.go`):

```go
type Provider interface {
    Type() string
    Discover(ctx context.Context, sys SystemContext) ([]Resource, error)
    Export(ctx context.Context, projectDir string, resources []Resource) ([]ProjectResource, error)
    Plan(ctx context.Context, desired []ProjectResource, current []Resource) ([]Action, error)
    Apply(ctx context.Context, projectDir string, action Action) error
    Validate(ctx context.Context, desired []ProjectResource) ([]ValidationResult, error)
}
```

- **`Type()`** returns the resource-type name (e.g. `"fonts"`). This is both
  the provider's registry key and the `resources/<type>.yaml` file name.
- **`Discover`** scans the live system and returns every resource it finds,
  each tagged with a `Confidence` (`ConfidenceHigh`/`Medium`/`Low`/`Unknown`)
  and a `Provenance` (where it came from). **Never guess**: if you can't
  confidently identify something, either don't report it, or report it with
  a confidence below `ConfidenceHigh` so `export` flags it for interactive
  review instead of silently including it.
- **`Export`** converts discovered `Resource`s into their project
  (`ProjectResource`) form. If your resource type has real file content to
  track (not just small attributes), write it under
  `project.FilesDir(projectDir)` — see `internal/providers/dotfiles` or
  `internal/providers/fonts`.
- **`Plan`** diffs desired vs. current and returns the `Action`s needed to
  reconcile them. Most providers do this purely by comparing `Attributes`
  (e.g. a content hash) — no filesystem or command access needed. Populate
  `Action.Attributes` with whatever `Apply` will need, so `Apply` never has
  to re-query the project.
- **`Apply`** executes a single `Action`. Keep it narrowly scoped to what
  the action says — the engine always recomputes the plan immediately
  before calling `Apply`, so there's no path to mutate the system without a
  fresh diff.
- **`Validate`** reports drift without modifying anything.

Study `internal/providers/fonts` first — it's the simplest complete
example (discover → hash-based diff → copy/refresh-cache → validate). For
providers that need to shell out to an external command,
`internal/providers/packages` and `internal/providers/gitrepos` show the
injectable-`commandRunner` pattern that keeps tests from touching the real
system.

## Optional interfaces

Two capabilities are opt-in via type assertion, not part of the core
`Provider` interface, since not every provider needs them:

- **`core.UserDeclaredProvider`** — implement `UserDeclared() bool` if your
  resources are entirely hand-authored rather than discovered (like
  `internal/providers/recipe`). The engine skips such providers during
  `export`, so it never overwrites hand-authored entries with an empty
  discovered list.
- **`core.DoctorProvider`** — implement
  `Doctor(ctx, projectDir, desired) ([]Diagnosis, error)` to add checks
  beyond drift detection (a broken symlink, an unreachable remote, a
  package no longer available). See `internal/providers/dotfiles`,
  `gitrepos`, or `packages` for examples.

## Registering a provider

Providers register themselves in `cmd/envsetup/main.go`:

```go
app.Registry.Register(yourprovider.New())
```

That's the only change needed outside your new package.

## Testing conventions

- Unit tests must never touch the real system. Providers that shell out
  take an injectable `commandRunner` function type with a real
  implementation (`execCommand`, using `os/exec`) and a `newWithRunner`
  constructor tests use to inject fixture output.
- Providers that only touch the filesystem (dotfiles, fonts) test directly
  against `t.TempDir()` — no mocking needed, since temp dirs are already
  fully isolated.
- Cover the full lifecycle in at least one test: discover → export → plan
  → apply → validate, including a drift scenario.
- If you want to verify against the real system too, keep destructive
  operations (`apply`) confined to a disposable environment (a throwaway
  resource you create and clean up yourself, or a container) — never run
  `apply` against a real, shared machine as part of development.

## General conventions

- **Small core, extensible ecosystem.** If you find yourself wanting to add
  resource-type-specific logic to `internal/core` or `internal/engine`,
  that's a signal it belongs in your provider instead.
- **Safe over clever.** `apply` only executes Create actions by default;
  Update and Delete require `--allow-update`/`--allow-remove`. Don't work
  around this in a provider — it's enforced centrally in the engine.
- **Human-readable over proprietary.** Keep `ProjectResource.Attributes`
  small and YAML-friendly. Store large content (file bytes) under
  `files/`, not base64-encoded in an attribute.
