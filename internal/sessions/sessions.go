// Package sessions lists saved toggle-popup tmux sessions.
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
	format  = "#{session_name}\t#{@" + popupshell.NamespaceOption + "}\t#{@" + popupshell.ScopeOption + "}\t#{@" + popupshell.EntrypointOption + "}"
)

type session struct {
	name, namespace, scope, entrypoint string
	clients                            string
}

// Run lists saved plugin sessions without attaching to or changing the tmux server.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: toggle-popup sessions")
		return 1
	}
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "sessions: tmux is required but was not found on PATH: %v\n", err)
		return 1
	}
	listed, err := tmuxOutput(tmuxPath, "list-sessions", "-F", format)
	if err != nil {
		// tmux reports an absent server as a failed read. Treat it like an empty
		// listing; importantly, never run a command that starts the server.
		listed = ""
	}
	sessions := parseSessions(listed)
	if len(sessions) > 0 {
		clients, err := tmuxOutput(tmuxPath, "list-clients", "-F", "#{client_session}")
		if err == nil {
			assignClientCounts(sessions, clients)
		}
	}
	_, _ = fmt.Fprintln(stdout, "namespace\tscope\tentrypoint\tsession\tclients")
	for _, item := range sessions {
		if item.namespace == "" || item.scope == "" || item.entrypoint == "" {
			item.namespace, item.scope, item.entrypoint = unknown, unknown, unknown
		}
		_, _ = fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\t%s\n", item.namespace, item.scope, item.entrypoint, item.name, item.clients)
	}
	return 0
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
	cmd := exec.Command(path, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return stdout.String(), nil
}
