package sessions

import (
	"fmt"
	"io"
	"os/exec"
	"slices"

	"github.com/maro114510/herdr-toggle-popup/internal/config"
	"github.com/maro114510/herdr-toggle-popup/internal/popupshell"
)

const (
	resetUsage = "usage: toggle-popup reset-current [--force]"
	forceFlag  = "--force"
)

// ResetCurrent discards the saved shell that belongs to the current context. It refuses a
// session that has attached clients unless --force is given, and never deletes any other session.
func ResetCurrent(args []string, stdout, stderr io.Writer) int {
	force, ok := parseResetArgs(args)
	if !ok {
		_, _ = fmt.Fprintln(stderr, resetUsage)
		return 1
	}

	identity, err := popupshell.ResolveIdentity(config.Load().Scope, popupshell.DefaultEntrypoint)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "reset-current: cannot identify the current context, so nothing was deleted: %v\n", err)
		return 1
	}
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "reset-current: tmux is required but was not found on PATH: %v\n", err)
		return 1
	}

	current, found := findSaved(tmuxPath, identity.Session)
	if !found {
		_, _ = fmt.Fprintf(stdout, "reset-current: nothing to delete, no saved shell exists for the current context (%s)\n", identity.Session)
		return 0
	}
	if current.clients != "0" && !force {
		_, _ = fmt.Fprintf(stderr, "reset-current: %s has %s attached client(s), so nothing was deleted; close them, or run reset-current %s to detach them and delete the shell\n",
			current.name, current.clients, forceFlag)
		return 1
	}

	// The leading = makes tmux match the name exactly instead of as a prefix.
	if _, err := tmuxOutput(tmuxPath, "kill-session", "-t", "="+current.name); err != nil {
		_, _ = fmt.Fprintf(stderr, "reset-current: could not delete %s: %v\n", current.name, err)
		return 1
	}
	detached := ""
	if current.clients != "0" {
		detached = fmt.Sprintf(" and detached %s attached client(s)", current.clients)
	}
	_, _ = fmt.Fprintf(stdout, "reset-current: deleted %s%s\n", current.name, detached)
	return 0
}

// parseResetArgs accepts no argument or exactly --force, and reports whether force was given.
func parseResetArgs(args []string) (force, ok bool) {
	switch {
	case len(args) == 0:
		return false, true
	case len(args) == 1 && args[0] == forceFlag:
		return true, true
	default:
		return false, false
	}
}

// findSaved returns the listed plugin session with exactly the given name and its client count.
// An absent server reads as a failed listing, so nothing is found; no command that starts the
// server runs. A failed client listing leaves the count unknown, which callers treat as attached.
func findSaved(tmuxPath, name string) (session, bool) {
	listed, _ := tmuxOutput(tmuxPath, "list-sessions", "-F", format)
	all := parseSessions(listed)
	index := slices.IndexFunc(all, func(item session) bool { return item.name == name })
	if index < 0 {
		return session{name: "", namespace: "", scope: "", entrypoint: "", clients: ""}, false
	}
	current := all[index : index+1]
	if clients, err := tmuxOutput(tmuxPath, "list-clients", "-F", "#{client_session}"); err == nil {
		assignClientCounts(current, clients)
	}
	return current[0], true
}
