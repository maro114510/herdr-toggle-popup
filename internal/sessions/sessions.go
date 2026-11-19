// Package sessions lists and, on explicit selection, deletes saved toggle-popup tmux sessions.
package sessions

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"github.com/maro114510/herdr-toggle-popup/internal/popupshell"
)

const (
	unknown = "unknown"
	format  = "#{session_name}\t#{" + popupshell.NamespaceOption + "}\t#{" + popupshell.ScopeOption + "}\t#{" + popupshell.EntrypointOption + "}"
)

type session struct {
	name, namespace, scope, entrypoint string
	clients                            string
}

const usage = "usage: toggle-popup sessions [delete [--force] <session>...]"

// Run lists saved plugin sessions without attaching to or changing the tmux server, or, with the
// delete subcommand, removes exactly the sessions it names.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) != 0 && args[0] != "delete" {
		_, _ = fmt.Fprintln(stderr, usage)
		return 1
	}
	if len(args) != 0 {
		return runDelete(args[1:], stdout, stderr)
	}
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "sessions: tmux is required but was not found on PATH: %v\n", err)
		return 1
	}
	sessions := loadSessions(tmuxPath)
	_, _ = fmt.Fprintln(stdout, "namespace\tscope\tentrypoint\tsession\tclients")
	for _, item := range sessions {
		if !item.identified() {
			item.namespace, item.scope, item.entrypoint = unknown, unknown, unknown
		}
		_, _ = fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\t%s\n", item.namespace, item.scope, item.entrypoint, item.name, item.clients)
	}
	return 0
}

// runDelete kills only the named sessions. It validates every target before killing any, so a
// rejected target leaves the others untouched. Selection is always explicit: no age, client count,
// or pattern ever selects a session.
func runDelete(args []string, stdout, stderr io.Writer) int {
	force := false
	var targets []string
	for _, arg := range args {
		switch {
		case arg == "--force":
			force = true
		case strings.HasPrefix(arg, "-"):
			_, _ = fmt.Fprintln(stderr, usage)
			return 1
		default:
			targets = append(targets, arg)
		}
	}
	if len(targets) == 0 {
		_, _ = fmt.Fprintln(stderr, usage)
		return 1
	}
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "sessions: tmux is required but was not found on PATH: %v\n", err)
		return 1
	}
	sessions := loadSessions(tmuxPath)
	byName := make(map[string]session, len(sessions))
	for _, item := range sessions {
		byName[item.name] = item
	}
	var selected []string
	seen := make(map[string]bool)
	rejected := false
	for _, name := range targets {
		if seen[name] {
			continue
		}
		seen[name] = true
		if reason := refusal(byName, name, force); reason != "" {
			_, _ = fmt.Fprintf(stderr, "sessions: refusing to delete %s: %s\n", name, reason)
			rejected = true
			continue
		}
		selected = append(selected, name)
	}
	if rejected {
		return 1
	}
	for _, name := range selected {
		// The leading = makes tmux match the name exactly, never as a prefix of another session.
		if _, err := tmuxOutput(tmuxPath, "kill-session", "-t", "="+name); err != nil {
			_, _ = fmt.Fprintf(stderr, "sessions: deleting %s failed: %v\n", name, err)
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "deleted\t%s\n", name)
	}
	return 0
}

// refusal explains why name may not be deleted, or returns "" when it may.
func refusal(byName map[string]session, name string, force bool) string {
	if !strings.HasPrefix(name, popupshell.SessionPrefix) {
		return "not a plugin session"
	}
	item, ok := byName[name]
	if !ok {
		return "not found on the dedicated server"
	}
	if !item.identified() {
		return "unknown ownership: legacy sessions are never adopted, remove them with tmux directly"
	}
	if force {
		return ""
	}
	if item.clients == unknown {
		return "attached clients could not be determined; use --force to delete anyway"
	}
	if item.clients != "0" {
		return "attached to " + item.clients + " client(s); detach first or use --force"
	}
	return ""
}

// loadSessions reads plugin sessions with their client counts, treating an absent server as an
// empty listing. It never runs a command that starts the server.
func loadSessions(tmuxPath string) []session {
	listed, err := tmuxOutput(tmuxPath, "list-sessions", "-F", format)
	if err != nil {
		// tmux reports an absent server as a failed read.
		listed = ""
	}
	sessions := parseSessions(listed)
	if len(sessions) > 0 {
		clients, err := tmuxOutput(tmuxPath, "list-clients", "-F", "#{client_session}")
		if err == nil {
			assignClientCounts(sessions, clients)
		}
	}
	return sessions
}

func (s session) identified() bool {
	return s.namespace != "" && s.scope != "" && s.entrypoint != ""
}

func parseSessions(raw string) []session {
	var result []session
	for line := range strings.SplitSeq(raw, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 4 || !strings.HasPrefix(fields[0], popupshell.SessionPrefix) {
			continue
		}
		result = append(result, session{name: fields[0], namespace: fields[1], scope: fields[2], entrypoint: fields[3], clients: unknown})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].name < result[j].name })
	return result
}

func assignClientCounts(sessions []session, raw string) {
	counts := make(map[string]int)
	for line := range strings.SplitSeq(raw, "\n") {
		if line != "" {
			counts[line]++
		}
	}
	for i := range sessions {
		sessions[i].clients = strconv.Itoa(counts[sessions[i].name])
	}
}

func tmuxOutput(path string, args ...string) (string, error) {
	args = append([]string{"-L", popupshell.SocketName, "-f", "/dev/null"}, args...)
	var stdout, stderr bytes.Buffer
	//nolint:gosec // path is the tmux binary resolved by the caller and args are fixed.
	cmd := exec.Command(path, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return stdout.String(), nil
}
