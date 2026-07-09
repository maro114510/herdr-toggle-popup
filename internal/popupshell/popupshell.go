// Package popupshell implements the `popup-shell` subcommand: a behavior-equivalent port of
// popup-shell.sh. It replaces the current process with the user's shell so the popup pane runs
// the shell directly rather than a Go parent process.
package popupshell

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
)

const (
	shellEnvVar  = "SHELL"
	defaultShell = "/bin/zsh"
)

type (
	lookPathFunc func(file string) (string, error)
	execFunc     func(argv0 string, argv, envv []string) error
)

// Run implements the `popup-shell` subcommand. It replaces the current process with
// ${SHELL:-/bin/zsh}, preserving environment and inheriting stdio. If the exec fails, it
// prints the error to stderr and returns non-zero; on success it never returns.
func Run(_ []string, stdout, stderr io.Writer) int {
	_ = stdout
	return run(stderr, exec.LookPath, syscall.Exec)
}

func run(stderr io.Writer, lookPath lookPathFunc, execProcess execFunc) int {
	shell := os.Getenv(shellEnvVar)
	if shell == "" {
		shell = defaultShell
	}

	path, err := lookPath(shell)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "popup-shell: %v\n", err)
		return 1
	}

	if err := execProcess(path, []string{shell}, os.Environ()); err != nil {
		_, _ = fmt.Fprintf(stderr, "popup-shell: %v\n", err)
		return 1
	}
	return 0
}
