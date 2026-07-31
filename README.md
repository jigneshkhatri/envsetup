<div align="center">

# EnvSetup

**Reproduce a Linux workstation from its exported state.**

</div>

EnvSetup captures the complete state of a Linux workstation — packages,
dotfiles, cloned repos, fonts, systemd services, and anything else you
declare by hand — into a plain-text, git-friendly project, and can recreate
that same state on another machine safely, predictably, and repeatedly.

It focuses on the **desired state** of a workstation, not the commands used
to reach it.

## Philosophy

- **Human-readable over proprietary.** Everything is YAML; tracked file
  content lives byte-for-byte in the project, not in a binary blob.
- **State-driven over command-driven.** You declare what should exist;
  EnvSetup figures out how to reconcile the difference.
- **Safe over clever.** By default, `apply` only fills in what's missing —
  it never overrides or removes configuration already on your machine
  unless you explicitly ask it to (see [Safety model](#safety-model)).
- **Correct over automatic.** Discovery never guesses. A resource EnvSetup
  isn't confident about is flagged for review, not silently included.
- **Small core, extensible ecosystem.** The core engine only understands
  generic resources — it has no idea what a package or a dotfile is.
  Everything ecosystem-specific lives in a provider (see
  [CONTRIBUTING.md](CONTRIBUTING.md)).

EnvSetup is **not** a package manager, a configuration language, or an
orchestration framework. The current scope is intentionally narrow: a single
Arch Linux workstation, one user's home directory. See
[Current scope](#current-scope) below.

## Installation

EnvSetup isn't packaged anywhere yet. Build it from source (requires the Go
version in [go.mod](go.mod) or newer):

```sh
git clone https://github.com/jigneshkhatri/envsetup.git
cd envsetup
go build -o envsetup ./cmd/envsetup
sudo mv envsetup /usr/local/bin/
```

## Quickstart

```sh
# See what EnvSetup can find on this machine, without changing anything.
envsetup scan

# Create a project and populate it from what scan found.
envsetup init ~/dotfiles
envsetup export ~/dotfiles

# ~/dotfiles is now a plain directory you can put under git. Commit it,
# push it, edit it by hand.
cd ~/dotfiles && git init && git add -A && git commit -m "Initial export"
```

On another machine (or after making changes to the project):

```sh
# Show exactly what would change -- never modifies anything.
envsetup plan

# Bring the workstation in line with the project.
envsetup apply

# Check whether the workstation has since drifted from the project.
envsetup validate

# Diagnose common problems (broken symlinks, unreachable git remotes,
# packages no longer available to install, ...).
envsetup doctor
```

Every command accepts `--project <dir>` (default: `$ENVSETUP_PROJECT`, or
the current directory).

## What gets tracked

| Type | Discovers | Notes |
|---|---|---|
| `packages` | Explicitly-installed pacman and AUR packages | No version pinning by default |
| `dotfiles` | A curated allowlist of well-known config paths under `$HOME` | Content stored byte-for-byte in the project |
| `git_repos` | Git-cloned tool/plugin checkouts in known container directories | Ref pinning is opt-in |
| `fonts` | Manually-installed fonts under `~/.local/share/fonts`, `~/.fonts` | Triggers a font-cache rebuild on apply |
| `services` | Enabled systemd units, user and system scope | Enablement only — never starts/stops a live service |
| `recipes` | Nothing — hand-authored only | The escape hatch for anything else; see below |

Discovery is deliberately conservative. If EnvSetup can't confidently
identify something (or a resource type has no way to represent "current
state" at all), it either flags it for review at export time or refuses to
guess entirely.

### Recipes: the escape hatch

Not everything can be discovered. For one-off setup steps EnvSetup has no
provider for, declare a recipe by hand in `resources/recipes.yaml`:

```yaml
- id: build-custom-tool
  check: test -x /usr/local/bin/custom-tool
  apply: |
    git clone https://example.com/custom-tool /tmp/custom-tool
    make -C /tmp/custom-tool install
```

`check` is required — it's how `plan`/`validate` know whether the recipe is
already satisfied without re-running it. `apply`'s output streams live to
your terminal as it runs, since recipes are the least "safe by construction"
part of the system.

## The exported project

```
my-workstation/
  envsetup.yaml            # project manifest: name, schema version
  resources/
    packages.yaml
    dotfiles.yaml
    git_repos.yaml
    fonts.yaml
    services.yaml
    recipes.yaml            # hand-authored; export never touches this file
  files/                    # tracked file content, byte-for-byte
    .config/nvim/init.lua
    .zshrc
```

`resources/*.yaml` is the desired state — human-reviewable, diffable, and
meant to be git-tracked. `files/` holds the actual bytes for any
content-bearing resource (dotfiles, fonts), so the project is
self-contained and portable on its own.

## Safety model

By default, `apply` only executes **create** actions — resources that are
declared in the project but missing from the workstation. It computes and
shows update/delete actions in the plan, but skips executing them:

```
$ envsetup apply
  + packages.neovim: install neovim (pacman)
  ~ dotfiles..zshrc: update .zshrc (content differs)
  - packages.cowsay: remove cowsay

1 action(s) skipped by default (pass --allow-update and/or --allow-remove to include them).

Apply these 1 action(s)? [y/N]
```

Pass `--allow-update` to let it overwrite resources that have drifted (e.g.
a dotfile whose content changed on disk), and `--allow-remove` to let it
remove resources that exist but aren't declared in the project (e.g.
uninstall a package). Both are opt-in, every time.

`plan`, `validate`, and `doctor` never modify anything, ever — they use exit
code `0` for "nothing to report", `2` for "found something", and `1` for an
error, which makes them safe to script.

## Current scope

The first version is intentionally narrow, per the project's philosophy of
building an exceptional experience for one workstation before generalizing:

- Arch Linux / pacman only — no cross-distribution support yet.
- A single user's home directory — no multi-machine orchestration.
- No secret management, no cloud sync, no GUI.

## Contributing

Adding support for a new resource type means implementing one interface —
see [CONTRIBUTING.md](CONTRIBUTING.md).

## License

[MIT](LICENSE)
