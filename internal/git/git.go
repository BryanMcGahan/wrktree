// Package git is a thin wrapper around the git binary.
package git

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Error is returned when a git command exits non-zero.
type Error struct {
	Args   []string
	Stderr string
	Stdout string
	Code   int
}

func (e *Error) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		msg = strings.TrimSpace(e.Stdout)
	}
	if msg == "" {
		msg = fmt.Sprintf("exit status %d", e.Code)
	}
	return fmt.Sprintf("git %s: %s", strings.Join(e.Args, " "), msg)
}

// Message returns the trimmed stderr (or stdout) of the failed command.
func (e *Error) Message() string {
	if s := strings.TrimSpace(e.Stderr); s != "" {
		return s
	}
	return strings.TrimSpace(e.Stdout)
}

// Run executes git in dir and returns trimmed stdout.
func Run(dir string, args ...string) (string, error) {
	out, err := RunRaw(dir, nil, args...)
	return strings.TrimRight(out, "\r\n"), err
}

// RunRaw executes git in dir, optionally feeding stdin, returning untrimmed stdout.
func RunRaw(dir string, stdin []byte, args ...string) (string, error) {
	return RunEnv(dir, stdin, nil, args...)
}

// RunEnv is RunRaw with additional environment variables.
func RunEnv(dir string, stdin []byte, env []string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	cmd.Env = append(cmd.Env, env...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		var ee *exec.ExitError
		code := -1
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
		return stdout.String(), &Error{Args: args, Stderr: stderr.String(), Stdout: stdout.String(), Code: code}
	}
	return stdout.String(), nil
}

// Succeeds reports whether the git command exits zero.
func Succeeds(dir string, args ...string) bool {
	_, err := RunRaw(dir, nil, args...)
	return err == nil
}

// ErrMessage extracts a human-friendly message from an error.
func ErrMessage(err error) string {
	var ge *Error
	if errors.As(err, &ge) {
		return ge.Message()
	}
	if err == nil {
		return ""
	}
	return err.Error()
}
