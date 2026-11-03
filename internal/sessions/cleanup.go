package sessions

import (
	"fmt"
	"io"
	"strings"

	"github.com/maro114510/herdr-toggle-popup/internal/popupshell"
)

const cleanupUsage = "usage: toggle-popup cleanup [--force] <session>..."

// RunCleanup implements the `cleanup` subcommand. It removes only the sessions a user names, and
// only after every named target is an identifiable plugin session. Nothing is removed when any
// target cannot be validated, so one typo never deletes a different shell.
func RunCleanup(args []string, stdout, stderr io.Writer) int {
	force, names, ok := parseCleanupArgs(args)
	if !ok || len(names) == 0 {
		_, _ = fmt.Fprintln(stderr, cleanupUsage)
		return 1
	}

	list, present, err := List()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "cleanup: %v\n", err)
		return 1
	}
	if !present {
		list = nil
	}

	targets, refusals := validateCleanupTargets(list, names, force)
	if len(refusals) > 0 {
		for _, refusal := range refusals {
			_, _ = fmt.Fprintf(stderr, "cleanup: %s\n", refusal)
		}
		return 1
	}

	failed := false
	for _, target := range targets {
		if err := Kill(target.Name); err != nil {
			_, _ = fmt.Fprintf(stderr, "cleanup: %v\n", err)
			failed = true
			continue
		}
		_, _ = fmt.Fprintf(stdout, "cleanup: removed %s\n", sanitizeText(target.Name))
	}
	if failed {
		return 1
	}
	return 0
}

// parseCleanupArgs accepts the optional --force flag and unique positional session names.
func parseCleanupArgs(args []string) (force bool, names []string, ok bool) {
	seen := make(map[string]bool, len(args))
	for _, arg := range args {
		switch {
		case arg == "--force":
			force = true
		case strings.HasPrefix(arg, "-"):
			return false, nil, false
		case seen[arg]:
			continue
		default:
			seen[arg] = true
			names = append(names, arg)
		}
	}
	return force, names, true
}

// validateCleanupTargets returns the sessions to remove and one refusal per target that must not
// be touched. An empty refusal list is the only case a caller may delete anything.
func validateCleanupTargets(list []Session, names []string, force bool) (targets []Session, refusals []string) {
	targets = make([]Session, 0, len(names))
	for _, name := range names {
		display := sanitizeText(name)
		if !popupshell.IsSessionName(name) {
			refusals = append(refusals, display+" is not a plugin session; refusing to remove it")
			continue
		}
		session, found := findSession(list, name)
		if !found {
			refusals = append(refusals, "no such saved session: "+display)
			continue
		}
		if !session.Identifiable() {
			refusals = append(refusals,
				display+" has unknown ownership (no recorded namespace, scope, or entrypoint); refusing to remove it")
			continue
		}
		if session.Clients > 0 && !force {
			refusals = append(refusals, fmt.Sprintf(
				"%s has %d attached client(s); close them or rerun with --force", display, session.Clients))
			continue
		}
		targets = append(targets, session)
	}
	return targets, refusals
}
