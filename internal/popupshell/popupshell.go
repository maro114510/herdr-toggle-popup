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
	shellEnvVar        = "SHELL"
	defaultShell       = "/bin/zsh"
	defaultEntrypoint  = "shell"
	shellBin           = "sh"
	tmuxBin            = "tmux"
	scopeDirectory     = "directory"
	scopeTab           = "tab"
	workspaceIDEnvVar  = "HERDR_WORKSPACE_ID"
	socketPathEnvVar   = "HERDR_SOCKET_PATH"
	sessionPrefix      = "herdr-toggle-popup-"
	namespaceHashBytes = 16
	sessionHashBytes   = 16
)

// tmuxAttachScript picks the popup's tmux target:
//   - dedicated session: attach and bind M-l to detach
//   - otherwise: create on the dedicated server, then attach
//
// It only ever looks for the namespaced session name on the dedicated server. Sessions named
// with a legacy identity (no namespace) are never attached to or removed, because identity now
// always includes the Herdr namespace hash.
const tmuxAttachScript = `if "$4" -L herdr-toggle-popup -f /dev/null has-session -t "$1" 2>/dev/null; then
  "$4" -L herdr-toggle-popup -f /dev/null bind-key -n M-l detach-client
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
	namespace, err := namespace()
	if err != nil {
		return "", "", err
	}

	cwd = herdr.ContextField("focused_pane_cwd")
	if cwd == "" {
		return "", "", errors.New("could not determine the focused pane's cwd")
	}

	if scopeMode == scopeDirectory {
		return fmt.Sprintf("namespace:%s:directory:%s:%s", namespace, cwd, entrypoint), cwd, nil
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
		return fmt.Sprintf("namespace:%s:tab:%s:%s:%s", namespace, workspaceID, tabID, entrypoint), cwd, nil
	}

	return fmt.Sprintf("namespace:%s:workspace:%s:%s", namespace, workspaceID, entrypoint), cwd, nil
}

// namespace returns a stable identifier for the current Herdr namespace, derived from
// HERDR_SOCKET_PATH. Two Herdr instances that expose different sockets therefore never share a
// saved shell, even when their workspace, tab, or focused-directory values are identical.
// It errors when HERDR_SOCKET_PATH is unavailable so startup fails before touching tmux instead
// of silently falling back to a legacy, un-namespaced identity.
func namespace() (string, error) {
	socketPath := os.Getenv(socketPathEnvVar)
	if socketPath == "" {
		return "", fmt.Errorf("%s must be set", socketPathEnvVar)
	}
	return namespaceForSocketPath(socketPath), nil
}

// namespaceForSocketPath hashes a raw socket path into the fixed-width namespace token used in
// the session identity.
func namespaceForSocketPath(socketPath string) string {
	sum := sha256.Sum256([]byte(socketPath))
	return hex.EncodeToString(sum[:namespaceHashBytes])
}

func sessionName(key string) string {
	sum := sha256.Sum256([]byte(key))
	return sessionPrefix + hex.EncodeToString(sum[:sessionHashBytes])
}
