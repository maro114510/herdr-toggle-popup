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
	force := false
	switch {
	case len(args) == 0:
	case len(args) == 1 && args[0] == forceFlag:
		force = true
	default:
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

	// An absent server reads as a failed listing and means there is nothing to delete; never run a
	// command that starts the server.
	listed, _ := tmuxOutput(tmuxPath, "list-sessions", "-F", format)
	found := parseSessions(listed)
	index := slices.IndexFunc(found, func(item session) bool { return item.name == identity.Session })
	if index < 0 {
		_, _ = fmt.Fprintf(stdout, "reset-current: nothing to delete, no saved shell exists for the current context (%s)\n", identity.Session)
		return 0
	}
	current := found[index : index+1]
	// A failed client listing leaves the count unknown, which is refused like an attached session.
	if clients, err := tmuxOutput(tmuxPath, "list-clients", "-F", "#{client_session}"); err == nil {
		assignClientCounts(current, clients)
	}
	name, clients := current[0].name, current[0].clients
	if clients != "0" && !force {
		_, _ = fmt.Fprintf(stderr, "reset-current: %s has %s attached client(s), so nothing was deleted; close them, or run reset-current %s to detach them and delete the shell\n",
			name, clients, forceFlag)
		return 1
	}

	// The leading = makes tmux match the name exactly instead of as a prefix.
	if _, err := tmuxOutput(tmuxPath, "kill-session", "-t", "="+name); err != nil {
		_, _ = fmt.Fprintf(stderr, "reset-current: could not delete %s: %v\n", name, err)
		return 1
	}
	if clients != "0" {
		_, _ = fmt.Fprintf(stdout, "reset-current: deleted %s and detached %s attached client(s)\n", name, clients)
		return 0
	}
	_, _ = fmt.Fprintf(stdout, "reset-current: deleted %s\n", name)
	return 0
}
