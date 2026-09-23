// Package popupshell implements the `popup-shell` subcommand. The Herdr popup pane is only a
// transient tmux client; the actual shell lives in a named tmux session so closing the Herdr pane
// removes all UI chrome without killing the shell session.
package popupshell

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"

	"github.com/maro114510/herdr-toggle-popup/internal/config"
	"github.com/maro114510/herdr-toggle-popup/internal/herdr"
)

const (
	shellEnvVar       = "SHELL"
	defaultShell      = "/bin/zsh"
	defaultEntrypoint = "shell"
	shellBin          = "sh"
	tmuxBin           = "tmux"
	scopeDirectory    = "directory"
	scopeTab          = "tab"
	workspaceIDEnvVar = "HERDR_WORKSPACE_ID"
	sessionPrefix     = "herdr-toggle-popup-"
	sessionHashBytes  = 16
)

// tmuxAttachScript picks the popup's tmux target:
//   - dedicated session: attach and bind M-l to detach
//   - legacy default-server session: attach as-is
//   - neither: create on the dedicated server, then attach
const tmuxAttachScript = `if "$4" -L herdr-toggle-popup -f /dev/null has-session -t "$1" 2>/dev/null; then
  "$4" -L herdr-toggle-popup -f /dev/null bind-key -n M-l detach-client
elif "$4" -f /dev/null has-session -t "$1" 2>/dev/null; then
  exec "$4" -f /dev/null attach-session -t "$1"
else
  "$4" -L herdr-toggle-popup -f /dev/null new-session -d -s "$1" -c "$2" "$3"
  "$4" -L herdr-toggle-popup -f /dev/null set-option -t "$1" status off
  "$4" -L herdr-toggle-popup -f /dev/null set-option -t "$1" mouse on
  "$4" -L herdr-toggle-popup -f /dev/null bind-key -n M-l detach-client
fi
exec "$4" -L herdr-toggle-popup -f /dev/null attach-session -t "$1"`

type (
	lookPathFunc func(file string) (string, error)
	execFunc     func(argv0 string, argv, envv []string) error
)

// Run replaces the current process with a tmux attach, creating the session first if needed.
// It inherits stdio and never returns on success.
func Run(args []string, stdout, stderr io.Writer) int {
	_ = stdout
	return run(args, stderr, exec.LookPath, syscall.Exec)
}

func run(args []string, stderr io.Writer, lookPath lookPathFunc, execProcess execFunc) int {
	entrypoint := defaultEntrypoint
	if len(args) > 0 && args[0] != "" {
		entrypoint = args[0]
	}

	cfg := config.Load()
	if !config.IsValidScope(cfg.Scope) {
		_, _ = fmt.Fprintf(stderr, "popup-shell: invalid scope %q in config; falling back to workspace scope\n", cfg.Scope)
	}
	sessionKey, cwd, err := tmuxSessionKey(cfg.Scope, entrypoint)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "popup-shell: %v\n", err)
		return 1
	}

	shPath, err := lookPath(shellBin)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "popup-shell: %v\n", err)
		return 1
	}

	tmuxPath, err := lookPath(tmuxBin)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "popup-shell: tmux is required but was not found on PATH: %v\n", err)
		return 1
	}

	shell := os.Getenv(shellEnvVar)
	if shell == "" {
		shell = defaultShell
	}
	shellPath, err := lookPath(shell)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "popup-shell: %v\n", err)
		return 1
	}

	argv := []string{shellBin, "-c", tmuxAttachScript, "popup-shell", sessionName(sessionKey), cwd, shellPath, tmuxPath}
	if err := execProcess(shPath, argv, os.Environ()); err != nil {
		_, _ = fmt.Fprintf(stderr, "popup-shell: %v\n", err)
		return 1
	}
	return 0
}

func tmuxSessionKey(scopeMode, entrypoint string) (key, cwd string, err error) {
	cwd = herdr.ContextField("focused_pane_cwd")
	if cwd == "" {
		return "", "", errors.New("could not determine the focused pane's cwd")
	}

	if scopeMode == scopeDirectory {
		return fmt.Sprintf("directory:%s:%s", cwd, entrypoint), cwd, nil
	}

	workspaceID := os.Getenv(workspaceIDEnvVar)
	if workspaceID == "" {
		workspaceID = herdr.ContextField("workspace_id")
	}
	if workspaceID == "" {
		return "", "", fmt.Errorf("%s must be set", workspaceIDEnvVar)
	}

	if scopeMode == scopeTab {
		tabID := herdr.ContextField("tab_id")
		if tabID == "" {
			return "", "", errors.New("could not determine the focused tab's id")
		}
		return fmt.Sprintf("tab:%s:%s:%s", workspaceID, tabID, entrypoint), cwd, nil
	}

	return fmt.Sprintf("workspace:%s:%s", workspaceID, entrypoint), cwd, nil
}

func sessionName(key string) string {
	sum := sha256.Sum256([]byte(key))
	return sessionPrefix + hex.EncodeToString(sum[:sessionHashBytes])
}
