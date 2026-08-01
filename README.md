<div align="center">

# EnvSetup

**Reproduce a Linux workstation from its exported state.**

[![CI](https://github.com/jigneshkhatri/envsetup/actions/workflows/ci.yml/badge.svg)](https://github.com/jigneshkhatri/envsetup/actions/workflows/ci.yml)
[![Go Reference](https://img.shields.io/badge/go-mod-blue)](go.mod)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

</div>

EnvSetup is a CLI tool that captures the complete state of a Linux
workstation — packages, dotfiles, cloned repos, fonts, systemd services, and
anything else you declare by hand — into a plain-text, git-friendly
**project**, and can recreate that same state on another machine safely,
predictably, and repeatedly.

It focuses on the **desired state** of a workstation, not the sequence of
commands used to reach it.

```
$ envsetup plan
  + packages.neovim: install neovim (pacman)
  ~ dotfiles..zshrc: update .zshrc (content differs)
  - packages.cowsay: remove cowsay

Plan: 1 to create, 1 to update, 1 to delete.
```

## Table of contents

- [Why EnvSetup](#why-envsetup)
- [How it works](#how-it-works)
- [What gets tracked](#what-gets-tracked)
- [Installation](#installation)
- [Usage](#usage)
- [Safety model](#safety-model)
- [Project layout](#project-layout)
- [Current scope](#current-scope)
- [Contributing](#contributing)
- [License](#license)

## Why EnvSetup

Rebuilding a Linux workstation usually means retracing dozens of manual
steps — installing packages, cloning repos, copying config, remembering
one-off tweaks — that were never written down anywhere. EnvSetup turns that
into a single, reviewable, version-controlled project:

- **Human-readable over proprietary.** Everything is YAML; tracked file
  content is stored byte-for-byte, not as a binary blob or a base64 string.
- **State-driven over command-driven.** You declare what should exist;
  EnvSetup figures out how to reconcile the difference.
- **Safe over clever.** By default, `apply` only fills in what's *missing*
  — it never overrides or removes configuration already on your machine
  unless you explicitly ask it to (see [Safety model](#safety-model)).
- **Correct over automatic.** Discovery never guesses. A resource EnvSetup
  isn't confident about is flagged for review, not silently included.
- **Small core, extensible ecosystem.** The core engine has no idea what a
  package or a dotfile is — that knowledge lives entirely in pluggable
  providers (see [Contributing](#contributing)).

EnvSetup is **not** a package manager, a configuration language, or an
orchestration framework.

## How it works

Everything EnvSetup manages is modeled as a **resource**: an application, a
dotfile, a cloned repo, a font, a systemd unit. Each resource type is owned
by a **provider**, a small pluggable module that knows how to:

| Step | What it does |
|---|---|
| **Discover** | Scan the live system for resources of its type, read-only |
| **Export** | Turn a discovered resource into the project's on-disk (desired-state) form |
| **Plan** | Diff desired vs. current and produce the actions needed to reconcile them |
| **Apply** | Execute one of those actions |
| **Validate** | Report drift between desired and current state, without changing anything |

The core engine drives every provider through this same lifecycle — it
never contains resource-specific logic itself. That's what makes the
`scan → export → plan → apply → validate` workflow behave identically
whether you're reconciling packages, dotfiles, or a systemd unit.

## What gets tracked

| Type | Discovers | Notes |
|---|---|---|
| `packages` | Explicitly-installed pacman and AUR packages | No version pinning by default |
| `flatpaks` | User-scope Flatpak app installs | Always `--user` scope, never needs root |
| `dotfiles` | A blanket scan of top-level `$HOME` dotfiles and `.config/*`, bounded by exclusion lists | Content stored byte-for-byte; each `.config/<app>` directory is one grouped resource |
| `system_configs` | `/etc` files pacman reports as locally modified, plus hand-placed files in known drop-in directories (`/etc/sddm.conf.d`, `/etc/sysctl.d`, `/etc/udev/rules.d`, ...) | Modified files via `pacman -Qii`; drop-in files filtered by pacman ownership; machine-identity files (`/etc/passwd`, `/etc/fstab`, ...) are excluded |
| `git_repos` | Git-cloned tool/plugin checkouts in known container directories | Ref pinning is opt-in |
| `fonts` | Manually-installed fonts under `~/.local/share/fonts`, `~/.fonts` | Triggers a font-cache rebuild on apply |
| `themes` | Manually-installed GTK/icon/cursor/SDDM themes, user and system-wide | System-wide themes are filtered by pacman ownership; can also reproduce which SDDM theme is active |
| `services` | Enabled systemd units, user and system scope | Enablement only — never starts/stops a live service |
| `recipes` | Nothing — hand-authored only | The escape hatch below |

Discovery is deliberately conservative. If EnvSetup can't confidently
identify something (or a resource type has no way to represent "current
state" at all, like a recipe), it either flags it for review at export time
or refuses to guess entirely.

<details>
<summary><strong>Recipes: the escape hatch</strong></summary>

<br>

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

</details>

## Installation

EnvSetup isn't packaged anywhere yet. Build it from source (requires the Go
version in [go.mod](go.mod) or newer):

```sh
git clone https://github.com/jigneshkhatri/envsetup.git
cd envsetup
go build -o envsetup ./cmd/envsetup
sudo mv envsetup /usr/local/bin/
```

## Usage

```sh
# See what EnvSetup can find on this machine, without changing anything.
envsetup scan

# Create a project and populate it from what scan found.
envsetup init ~/dotfiles
envsetup export ~/dotfiles

# ~/dotfiles is now a plain directory you can put under git.
cd ~/dotfiles && git init && git add -A && git commit -m "Initial export"
```

On another machine (or after editing the project by hand):

```sh
envsetup plan       # show exactly what would change -- never modifies anything
envsetup apply      # bring the workstation in line with the project
envsetup validate   # check whether the workstation has since drifted
envsetup doctor     # diagnose common problems (broken symlinks, dead remotes, ...)
```

| Command | Modifies the system? | Description |
|---|---|---|
| `envsetup init [path]` | No | Scaffold a new, empty project |
| `envsetup scan` | No | Discover resources on this workstation |
| `envsetup export [path]` | No | Write a project from what `scan` found |
| `envsetup plan` | No | Show what `apply` would do |
| `envsetup apply` | **Yes** | Reconcile the workstation with the project |
| `envsetup validate` | No | Detect drift from the project |
| `envsetup doctor` | No | Diagnose common problems |

Every command accepts `--project <dir>` (default: `$ENVSETUP_PROJECT`, or the
current directory). `plan`, `validate`, and `doctor` exit `0` for "nothing to
report", `2` for "found something", and `1` for an error, so they're safe to
script.

## Safety model

By default, `apply` only executes **create** actions — resources that are
declared in the project but missing from the workstation. It still computes
and *shows* update/delete actions in the plan, but skips executing them:

```
$ envsetup apply
  + packages.neovim: install neovim (pacman)
  ~ dotfiles..zshrc: update .zshrc (content differs)
  - packages.cowsay: remove cowsay

1 action(s) skipped by default (pass --allow-update and/or --allow-remove to include them).

Apply these 1 action(s)? [y/N]
```

Pass `--allow-update` to let it overwrite resources that have drifted (e.g. a
dotfile whose content changed on disk), and `--allow-remove` to let it remove
resources that exist but aren't declared in the project (e.g. uninstall a
package). Both are opt-in, every time.

## Project layout

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
content-bearing resource (dotfiles, fonts), so the project is self-contained
and portable on its own.

## Current scope

The first version is intentionally narrow, per the project's philosophy of
building an exceptional experience for one workstation before generalizing:

- Arch Linux / pacman only — no cross-distribution support yet.
- A single user's home directory — no multi-machine orchestration.
- No secret management, no cloud sync, no GUI.

## Contributing

Adding support for a new resource type means implementing one interface.
See [CONTRIBUTING.md](CONTRIBUTING.md) for the provider contract, testing
conventions, and where to look for examples.

## License

[MIT](LICENSE)
