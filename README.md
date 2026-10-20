# wrk

A terminal app that makes git worktrees simple: create, delete, search and jump
between them, then commit (down to individual lines), merge locally and open
pull requests without leaving the terminal.

```
 wrk · myapp · 4 worktrees · base main

▌● main         ✓ clean          =         2h   Merge branch 'feat/login'  [main]
   feat/search  ● +1 ~2 ?1       ↑2        5m   Add fuzzy matcher
   bugfix-42    ✓ clean          —         1d   Fix null deref
   pr-118       ✓ clean          =         3d   Bump deps

 enter cd · / search · n new · d delete · c commit · m merge · P PR · o open · ? more
```

## Install

```sh
go install github.com/BryanMcGahan/wrktree/cmd/wrk@latest
```

Then add shell integration so `wrk` can change your directory, plus completions:

```sh
# ~/.zshrc
eval "$(wrk shell-init zsh)"
# ~/.bashrc
eval "$(wrk shell-init bash)"
# ~/.config/fish/config.fish
wrk shell-init fish | source
```

## Usage

Run `wrk` with no arguments to open the interactive interface, or use subcommands:

| Command | What it does |
|---|---|
| `wrk` | Interactive UI: list, fuzzy search, create, delete, commit, merge, PR, prune |
| `wrk ls [--json]` | List worktrees with dirty state, ahead/behind and last commit |
| `wrk new <branch>` | New branch from the base branch, or check out an existing local branch |
| `wrk new origin/<branch>` | Track a remote branch |
| `wrk new --pr 123` / `wrk new '#123'` | Check out a pull request (uses `gh` for the branch name if installed) |
| `wrk rm [worktree] [-f [-f]]` | Exactly like `git worktree remove`: refuses dirty/locked worktrees without `--force`, keeps the branch |
| `wrk switch [query]` / `wrk cd` | Fuzzy-pick a worktree and cd into it |
| `wrk open [worktree]` | Open in your editor |
| `wrk commit [-m msg]` | Commit UI with file, hunk and line staging (or a quick commit with `-m`) |
| `wrk merge [source] [--into branch] [-s strategy]` | Merge locally: `merge` (--no-ff), `ff`, `squash` or `rebase` |
| `wrk merge --continue` / `--abort` | Finish or cancel a merge stopped on conflicts |
| `wrk pr [worktree] [--base branch]` | Push if needed and open the host's "new pull request" page |
| `wrk prune [--stale 30d] [-n]` | Remove worktrees whose branch is merged, stale or whose directory is gone |
| `wrk init` | Write a starter `.wrk.toml` |
| `wrk config` | Show effective configuration and its sources |

### Commit interface

`c` in the list (or `wrk commit`) opens a three-pane view: files, diff, message.

- **Files:** `space` stage/unstage a file · `a` stage all · `u` unstage all · `enter` go to lines
- **Diff:** `space` stage/unstage the line under the cursor · `v` select a range · `a` whole hunk · `n`/`N` next/previous hunk
- **Message:** `i` to type · `tab` subject → body · `ctrl+e` / `e` write it in `$EDITOR` · `A` amend
- `ctrl+s` commits

Untracked files can be staged line by line too (wrk marks them with `git add -N` first).

### Merging

`m` in the list (or `wrk merge`) merges the selected worktree's branch into
`base_branch` by default. Press `t` to pick any other local or remote branch
(e.g. `origin/release`); a local tracking branch is created if needed.

The merge runs in the worktree where the target branch is checked out; if it
isn't checked out anywhere, a temporary worktree is used (fast-forwards just
move the ref). Conflicts get a resolution screen: open files in your editor,
mark them resolved, then continue — or abort to restore everything.

Afterwards wrk offers to push the target and remove the merged worktree.

### Pull requests

wrk doesn't need API tokens: it pushes the branch (setting upstream if needed)
and opens your remote's "create pull request" page. GitHub (incl. Enterprise),
GitLab, Bitbucket and Azure DevOps are recognised from the remote URL; for
anything else set `pr_url` in `.wrk.toml`.

## Configuration

Settings merge in this order (later wins):

1. `~/.config/wrk/config.toml` — personal defaults
2. `<repo>/.wrk.toml` — project settings, commit this
3. `<repo>/.wrk.local.toml` — personal per-project overrides, gitignore this

```toml
# Where new worktrees go. Relative paths are resolved from the main worktree.
# Variables: {repo} {branch} {branch_slug} {home}
path = "../{repo}-worktrees/{branch_slug}"   # default

base_branch = "main"     # default: origin/HEAD, then main/master
remote = "origin"
editor = "code"          # default: $VISUAL / $EDITOR
# pr_url = "{web}/compare/{base}...{branch}"   # for unrecognised hosts

[hooks]
copy = [".env", ".env.local"]       # copied from the main worktree (globs ok)
symlink = ["node_modules/.cache"]   # symlinked from the main worktree
post_create = ["npm install"]       # run in the new worktree
pre_remove = []                     # run before removing a worktree
```

Hooks always ask before running (Run / Skip / Abort) and show exactly what
they will do. Pass `--yes` to run them non-interactively or `--no-hooks` to skip.

## Development

```sh
make            # build bin/wrk
make run        # build and open the TUI
make run ARGS="ls --json"
make install    # go install with version info
make check      # gofmt check, vet and tests
make help       # list all targets
```
# wrktree
