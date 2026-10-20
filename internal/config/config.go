// Package config loads wrk configuration from global and per-project files.
//
// Precedence (later wins): built-in defaults, ~/.config/wrk/config.toml,
// <repo>/.wrk.toml, <repo>/.wrk.local.toml.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	ProjectFile = ".wrk.toml"
	LocalFile   = ".wrk.local.toml"
	DefaultPath = "../{repo}-worktrees/{branch_slug}"
)

type Hooks struct {
	Copy       []string `toml:"copy"`
	Symlink    []string `toml:"symlink"`
	PostCreate []string `toml:"post_create"`
	PreRemove  []string `toml:"pre_remove"`
}

type Config struct {
	Path       string `toml:"path"`
	BaseBranch string `toml:"base_branch"`
	Remote     string `toml:"remote"`
	Editor     string `toml:"editor"`
	PRURL      string `toml:"pr_url"`
	Hooks      Hooks  `toml:"hooks"`

	// Sources lists the files that were loaded, for display.
	Sources []string `toml:"-"`
}

// file mirrors Config but with pointers so we can tell "unset" from "empty".
type file struct {
	Path       *string `toml:"path"`
	BaseBranch *string `toml:"base_branch"`
	Remote     *string `toml:"remote"`
	Editor     *string `toml:"editor"`
	PRURL      *string `toml:"pr_url"`
	Hooks      struct {
		Copy       *[]string `toml:"copy"`
		Symlink    *[]string `toml:"symlink"`
		PostCreate *[]string `toml:"post_create"`
		PreRemove  *[]string `toml:"pre_remove"`
	} `toml:"hooks"`
}

// GlobalPath returns the location of the global config file.
func GlobalPath() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "wrk", "config.toml")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "wrk", "config.toml")
}

// Load reads configuration for a repository whose main worktree is root.
func Load(root string) (*Config, error) {
	c := &Config{Path: DefaultPath}
	paths := []string{GlobalPath()}
	if root != "" {
		paths = append(paths, filepath.Join(root, ProjectFile), filepath.Join(root, LocalFile))
	}
	for _, p := range paths {
		if err := c.apply(p); err != nil {
			return nil, err
		}
	}
	return c, nil
}

func (c *Config) apply(path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var f file
	if _, err := toml.Decode(string(data), &f); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	set := func(dst *string, src *string) {
		if src != nil {
			*dst = *src
		}
	}
	setL := func(dst *[]string, src *[]string) {
		if src != nil {
			*dst = *src
		}
	}
	set(&c.Path, f.Path)
	set(&c.BaseBranch, f.BaseBranch)
	set(&c.Remote, f.Remote)
	set(&c.Editor, f.Editor)
	set(&c.PRURL, f.PRURL)
	setL(&c.Hooks.Copy, f.Hooks.Copy)
	setL(&c.Hooks.Symlink, f.Hooks.Symlink)
	setL(&c.Hooks.PostCreate, f.Hooks.PostCreate)
	setL(&c.Hooks.PreRemove, f.Hooks.PreRemove)
	c.Sources = append(c.Sources, path)
	return nil
}

// EditorCommand returns the editor command split into argv.
func (c *Config) EditorCommand() []string {
	for _, e := range []string{c.Editor, os.Getenv("VISUAL"), os.Getenv("EDITOR")} {
		if f := strings.Fields(e); len(f) > 0 {
			return f
		}
	}
	return []string{"vi"}
}

// Starter is the content written by `wrk init`.
const Starter = `# wrk project configuration. Commit this file to share it with your team.
# Personal overrides can go in .wrk.local.toml (add it to .gitignore).

# Where new worktrees are created. Relative paths are resolved from the main
# worktree. Variables: {repo} {branch} {branch_slug} {home}
path = "../{repo}-worktrees/{branch_slug}"

# Default branch new worktrees start from and merges / PRs target.
# Leave empty to auto-detect (origin/HEAD, then main, then master).
base_branch = ""

# Remote used for pushing and PR links (default: origin).
# remote = "origin"

# Editor used by "open" (falls back to $VISUAL / $EDITOR).
# editor = "code"

# Custom PR creation URL for hosts wrk doesn't recognise.
# Variables: {web} {owner} {repo} {branch} {base}
# pr_url = "{web}/compare/{base}...{branch}"

[hooks]
# Files copied from the main worktree into new worktrees.
copy = []
# Paths symlinked from the main worktree into new worktrees.
symlink = []
# Shell commands run inside a new worktree (you are asked every time).
post_create = []
# Shell commands run inside a worktree before it is removed.
pre_remove = []
`
