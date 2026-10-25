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
	// SocketName is the dedicated tmux server socket that holds saved popup shells.
	SocketName = "herdr-toggle-popup"
	// CloseKeyOption is the session option that records the key closing that session.
	CloseKeyOption = "@herdr-close-key"
	// DefaultEntrypoint is the manifest's popup entrypoint.
	DefaultEntrypoint = "shell"

	shellEnvVar        = "SHELL"
	defaultShell       = "/bin/zsh"
	defaultEntrypoint  = DefaultEntrypoint
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

// SessionIdentity is the saved-shell identity resolved from the plugin environment.
type SessionIdentity struct {
	Namespace string
	Key       string
	Session   string
	Cwd       string
}

// ResolveIdentity returns the identity of the saved shell for scopeMode and entrypoint.
// Namespace is populated even when a later input is missing, so a diagnostic can still report it.
func ResolveIdentity(scopeMode, entrypoint string) (SessionIdentity, error) {
	namespace, err := namespace()
	if err != nil {
		return SessionIdentity{}, err
	}

	key, cwd, err := tmuxSessionKey(scopeMode, entrypoint)
	if err != nil {
		return SessionIdentity{Namespace: namespace, Key: "", Session: "", Cwd: ""}, err
	}
	return SessionIdentity{Namespace: namespace, Key: key, Session: sessionName(key), Cwd: cwd}, nil
}

// tmuxAttachScript creates or reuses the popup session on the dedicated server, then attaches.
// It sets set-clipboard=external on every connection so mouse copies reach the outer terminal.
// The external value also stops a pane OSC52 sequence from filling a tmux buffer.
// The close key is bound in the root table so tmux keeps its default mouse and prefix bindings.
// Root is server-wide, but the command runs in the context of the client that pressed the key.
// The binding reads the session's @herdr-close-key option and detaches only that session's client.
// A key owned by another popup is forwarded to the pane instead of being swallowed.
// A name tmux rejects falls back to the default so the popup can always be closed.
// The key and binding are reapplied on every connection, so a change applies on the next open.
// The window size is explicitly the smallest attached client's, because the default policy differs
// between supported tmux versions; it is reapplied on every connection to correct any drift.
// Legacy sessions without the namespace are never attached to or removed.
const tmuxAttachScript = `if "$4" -L herdr-toggle-popup -f /dev/null has-session -t "$1" 2>/dev/null; then
  "$4" -L herdr-toggle-popup -f /dev/null set-option -s set-clipboard external
else
  "$4" -L herdr-toggle-popup -f /dev/null new-session -d -s "$1" -c "$2" "$3"
  "$4" -L herdr-toggle-popup -f /dev/null set-option -s set-clipboard external
  "$4" -L herdr-toggle-popup -f /dev/null set-option -t "$1" status off
  "$4" -L herdr-toggle-popup -f /dev/null set-option -t "$1" mouse on
fi
"$4" -L herdr-toggle-popup -f /dev/null set-option -w -t "$1" window-size smallest
key="$5"
"$4" -L herdr-toggle-popup -f /dev/null set-option -t "$1" @herdr-close-key "$key"
if ! "$4" -L herdr-toggle-popup -f /dev/null bind-key -n "$key" if-shell -F "#{==:#{@herdr-close-key},$key}" "detach-client" "send-keys $key"; then
  key=M-l
  "$4" -L herdr-toggle-popup -f /dev/null set-option -t "$1" @herdr-close-key "$key"
  "$4" -L herdr-toggle-popup -f /dev/null bind-key -n "$key" if-shell -F "#{==:#{@herdr-close-key},$key}" "detach-client" "send-keys $key"
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

// loadConfig reads config.toml and reports every fallback warning to stderr with the command
// prefix, including an unrecognized scope that falls back to workspace.
func loadConfig(stderr io.Writer) config.Config {
	cfg := config.Load()
	for _, warning := range cfg.Warnings {
		_, _ = fmt.Fprintf(stderr, "popup-shell: %s\n", warning)
	}
	if !config.IsValidScope(cfg.Scope) {
		_, _ = fmt.Fprintf(stderr, "popup-shell: invalid scope %q in config; falling back to workspace scope\n", cfg.Scope)
	}
	return cfg
}

func run(args []string, stderr io.Writer, lookPath lookPathFunc, execProcess execFunc) int {
	entrypoint := defaultEntrypoint
	if len(args) > 0 && args[0] != "" {
		entrypoint = args[0]
	}

	cfg := loadConfig(stderr)
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

	argv := []string{shellBin, "-c", tmuxAttachScript, "popup-shell", sessionName(sessionKey), cwd, shellPath, tmuxPath, cfg.CloseKey}
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
