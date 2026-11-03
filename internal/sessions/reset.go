package sessions

import (
	"fmt"
	"io"
	"os/exec"

	"github.com/maro114510/herdr-toggle-popup/internal/config"
	"github.com/maro114510/herdr-toggle-popup/internal/popupshell"
)

const resetCurrentUsage = "usage: toggle-popup reset-current [--force]"

// RunResetCurrent implements the `reset-current` subcommand. It removes only the saved shell the
// current namespace, scope, and entrypoint resolve to, and refuses to remove one with an attached
// client unless --force is given.
func RunResetCurrent(args []string, stdout, stderr io.Writer) int {
	force, ok := parseForce(args)
	if !ok {
		_, _ = fmt.Fprintln(stderr, resetCurrentUsage)
		return 1
	}

	cfg := loadConfig("reset-current", stderr)
	identity, err := popupshell.ResolveIdentity(cfg.Scope, popupshell.DefaultEntrypoint)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "reset-current: %v\n", err)
		return 1
	}

	list, present, err := List()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "reset-current: %v\n", err)
		return 1
	}
	target, found := findSession(list, identity.Session)
	if !present || !found {
		_, _ = fmt.Fprintln(stdout, "reset-current: no saved shell for this context")
		return 0
	}

	if target.Clients > 0 && !force {
		_, _ = fmt.Fprintf(stderr,
			"reset-current: saved shell %s has %d attached client(s); close them or rerun with --force\n",
			sanitizeText(target.Name), target.Clients)
		return 1
	}

	if err := Kill(target.Name); err != nil {
		_, _ = fmt.Fprintf(stderr, "reset-current: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "reset-current: discarded saved shell %s\n", sanitizeText(target.Name))
	return 0
}

// parseForce accepts only the optional --force flag. ok is false for any other argument.
func parseForce(args []string) (force, ok bool) {
	for _, arg := range args {
		if arg != "--force" {
			return false, false
		}
		force = true
	}
	return force, true
}

func findSession(list []Session, name string) (Session, bool) {
	for _, session := range list {
		if session.Name == name {
			return session, true
		}
	}
	return Session{}, false
}

// loadConfig reads config.toml and reports every fallback warning to stderr with the command
// prefix. An unrecognized scope falls back to workspace, matching popup-shell's effective scope.
func loadConfig(command string, stderr io.Writer) config.Config {
	cfg := config.Load()
	for _, warning := range cfg.Warnings {
		_, _ = fmt.Fprintf(stderr, "%s: %s\n", command, warning)
	}
	if !config.IsValidScope(cfg.Scope) {
		_, _ = fmt.Fprintf(stderr, "%s: invalid scope %q in config; falling back to workspace scope\n", command, cfg.Scope)
		cfg.Scope = "workspace"
	}
	return cfg
}

// Kill removes the named session from the dedicated server.
func Kill(name string) error {
	tmuxPath, err := exec.LookPath(tmuxBin)
	if err != nil {
		return fmt.Errorf("tmux is required but was not found on PATH: %w", err)
	}
	_, stderr, runErr := run(tmuxPath, "kill-session", "-t", name)
	if runErr != nil {
		return commandError("could not remove "+name, stderr, runErr)
	}
	return nil
}
