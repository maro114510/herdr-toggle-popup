// Package cleanup implements the `cleanup` subcommand: inspecting saved popup sessions on the
// dedicated tmux server and deleting only the ones a user selects explicitly.
package cleanup

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/maro114510/herdr-toggle-popup/internal/popupshell"
)

const (
	tmuxBin           = "tmux"
	commandTimeout    = 3 * time.Second
	sessionFieldCount = 3

	attachedState = "attached"
	detachedState = "detached"

	// listFormat asks for the session name, its attached client count, and the ownership marker
	// in one read. A session without the marker reports an empty third field.
	listFormat = "#{session_name}\t#{session_attached}\t" + popupshell.OwnerOption
)

const usage = `usage: toggle-popup cleanup list
       toggle-popup cleanup delete [--force] <session>...`

// verdict is what the plugin knows about a session's ownership.
type verdict int

const (
	ownershipManaged verdict = iota
	ownershipNonPlugin
	ownershipLegacy
	ownershipOther
)

type session struct {
	name    string
	clients int
	owner   string
}

// Run implements the `cleanup` subcommand.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usageError(stderr)
	}

	switch args[0] {
	case "list":
		return runList(args[1:], stdout, stderr)
	case "delete":
		return runDelete(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "cleanup: unknown action %q\n", args[0])
		return usageError(stderr)
	}
}

func usageError(stderr io.Writer) int {
	_, _ = fmt.Fprintln(stderr, usage)
	return 1
}

// runList prints every session the plugin owns and nothing else. It never changes a session, so a
// candidate can be inspected as often as needed before it is selected for deletion.
func runList(args []string, stdout, stderr io.Writer) int {
	if len(args) != 0 {
		return usageError(stderr)
	}

	sessions, err := listSessions()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "cleanup: %v\n", err)
		return 1
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].name < sessions[j].name })

	candidates := 0
	unmanaged := 0
	for _, s := range sessions {
		if classify(s) == ownershipManaged {
			candidates++
		} else {
			unmanaged++
		}
	}

	_, _ = fmt.Fprintf(stdout, "candidates: %d\n", candidates)
	for _, s := range sessions {
		if classify(s) != ownershipManaged {
			continue
		}
		state := detachedState
		if s.clients > 0 {
			state = attachedState
		}
		_, _ = fmt.Fprintf(stdout, "%s clients=%d %s\n", s.name, s.clients, state)
	}
	_, _ = fmt.Fprintf(stdout, "unmanaged: %d\n", unmanaged)
	return 0
}

// runDelete keeps the explicit-selection contract: it validates the whole selection first, so a
// target the plugin must not touch leaves every other session in place.
func runDelete(_ []string, _ io.Writer, stderr io.Writer) int {
	return usageError(stderr)
}

// listSessions reads the dedicated server without starting it. An absent server is an empty list,
// not an error, because a user inspecting candidates must not create a server as a side effect.
func listSessions() ([]session, error) {
	tmuxPath, err := exec.LookPath(tmuxBin)
	if err != nil {
		return nil, fmt.Errorf("tmux is required but was not found on PATH: %w", err)
	}

	stdout, stderr, err := tmuxCommand(tmuxPath, "list-sessions", "-F", listFormat)
	if err != nil {
		if serverAbsent(stderr) {
			return nil, nil
		}
		detail := strings.TrimSpace(stderr)
		if detail == "" {
			detail = err.Error()
		}
		return nil, fmt.Errorf("could not list sessions: %s", detail)
	}

	sessions := make([]session, 0, strings.Count(stdout, "\n"))
	for line := range strings.SplitSeq(stdout, "\n") {
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", sessionFieldCount)
		if len(fields) != sessionFieldCount {
			return nil, fmt.Errorf("could not parse tmux session list entry %q", line)
		}
		clients, convErr := strconv.Atoi(fields[1])
		if convErr != nil {
			return nil, fmt.Errorf("could not parse the client count for session %q: %w", fields[0], convErr)
		}
		sessions = append(sessions, session{name: fields[0], clients: clients, owner: fields[2]})
	}
	return sessions, nil
}

// classify decides whether the plugin may manage a session. A name prefix alone is not ownership:
// sessions created before the ownership marker existed share it, so only an exact marker value
// makes a session a cleanup candidate.
func classify(s session) verdict {
	if !strings.HasPrefix(s.name, popupshell.SessionPrefix) {
		return ownershipNonPlugin
	}
	switch s.owner {
	case popupshell.OwnerValue:
		return ownershipManaged
	case "":
		return ownershipLegacy
	default:
		return ownershipOther
	}
}

// serverAbsent reports whether tmux failed only because the dedicated server is not running.
func serverAbsent(stderr string) bool {
	return strings.Contains(stderr, "no server running") ||
		strings.Contains(stderr, "No such file or directory")
}

// tmuxCommand runs a bounded tmux command on the plugin's dedicated server.
func tmuxCommand(tmuxPath string, args ...string) (stdout, stderr string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	serverArgs := append([]string{"-L", popupshell.SocketName, "-f", "/dev/null"}, args...)
	//nolint:gosec // tmuxPath comes from exec.LookPath for a fixed tool name, and every argument is built here.
	cmd := exec.CommandContext(ctx, tmuxPath, serverArgs...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err = cmd.Run()
	if ctx.Err() != nil {
		err = fmt.Errorf("%w after %s", ctx.Err(), commandTimeout)
	}
	return outBuf.String(), errBuf.String(), err
}
