// Package sessions implements the saved-shell management commands: sessions, reset-current, and
// cleanup. They read and change only the plugin's dedicated tmux server.
package sessions

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
	"unicode"

	"github.com/maro114510/herdr-toggle-popup/internal/popupshell"
)

const (
	tmuxBin           = "tmux"
	commandTimeout    = 5 * time.Second
	sessionFieldCount = 5

	// listSessionsFormat reads the session name and the ownership metadata popupshell records on
	// every connection. A session created before that metadata existed reports empty options.
	listSessionsFormat = "#{session_name}\t#{@herdr-namespace}\t#{@herdr-scope}\t#{@herdr-entrypoint}\t#{session_attached}"

	// noServerMarker is what tmux reports when the dedicated server has never started. Listing
	// must treat it as an empty result, not as a failure, and must not start the server.
	noServerMarker = "no server running"
	// noServerConnectionMarker and missingSocketMarker are the macOS report for an absent server
	// whose socket directory has never been created.
	noServerConnectionMarker = "error connecting to"
	missingSocketMarker      = "No such file or directory"
)

// Session is one saved shell on the dedicated tmux server.
type Session struct {
	Name       string
	Namespace  string
	Scope      string
	Entrypoint string
	Clients    int
}

// Identifiable reports whether every ownership field was recorded for the session. A session
// without one is a legacy or unknown-ownership session, never a management target.
func (s Session) Identifiable() bool {
	return s.Namespace != "" && s.Scope != "" && s.Entrypoint != ""
}

// RunList implements the `sessions` subcommand. It never attaches, starts a server, or deletes
// anything.
func RunList(args []string, stdout, stderr io.Writer) int {
	if len(args) != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: toggle-popup sessions")
		return 1
	}

	list, present, err := List()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "sessions: %v\n", err)
		return 1
	}
	if !present {
		_, _ = fmt.Fprintln(stdout, "sessions: none (tmux server absent)")
		return 0
	}
	if len(list) == 0 {
		_, _ = fmt.Fprintln(stdout, "sessions: none")
		return 0
	}

	_, _ = fmt.Fprintf(stdout, "sessions: %d\n", len(list))
	for _, session := range list {
		writeSession(stdout, session)
	}
	return 0
}

func writeSession(stdout io.Writer, session Session) {
	if !session.Identifiable() {
		_, _ = fmt.Fprintf(stdout, "session: name=%s, ownership=unknown, clients=%d\n",
			sanitizeText(session.Name), session.Clients)
		return
	}
	_, _ = fmt.Fprintf(stdout, "session: name=%s, namespace=%s, scope=%s, entrypoint=%s, clients=%d\n",
		sanitizeText(session.Name), sanitizeText(session.Namespace), sanitizeText(session.Scope),
		sanitizeText(session.Entrypoint), session.Clients)
}

// List returns the plugin sessions on the dedicated server, sorted by name. present is false when
// the server has not started; that is not an error, and List never starts it.
func List() (sessions []Session, present bool, err error) {
	tmuxPath, err := exec.LookPath(tmuxBin)
	if err != nil {
		return nil, false, fmt.Errorf("tmux is required but was not found on PATH: %w", err)
	}

	stdout, stderr, runErr := run(tmuxPath, "list-sessions", "-F", listSessionsFormat)
	if runErr != nil {
		if serverAbsent(string(stderr)) {
			return nil, false, nil
		}
		return nil, false, commandError("could not list saved sessions", stderr, runErr)
	}

	sessions, err = parseSessions(string(stdout))
	if err != nil {
		return nil, true, err
	}
	return sessions, true, nil
}

func parseSessions(stdout string) ([]Session, error) {
	var sessions []Session
	for line := range strings.SplitSeq(stdout, "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != sessionFieldCount {
			return nil, fmt.Errorf("could not read saved sessions: expected %d fields, got %d", sessionFieldCount, len(fields))
		}
		if !popupshell.IsSessionName(fields[0]) {
			continue
		}
		clients, err := strconv.Atoi(fields[4])
		if err != nil {
			return nil, fmt.Errorf("could not read the client count for %s: %w", sanitizeText(fields[0]), err)
		}
		sessions = append(sessions, Session{
			Name:       fields[0],
			Namespace:  fields[1],
			Scope:      fields[2],
			Entrypoint: fields[3],
			Clients:    clients,
		})
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].Name < sessions[j].Name })
	return sessions, nil
}

// run executes one tmux command against the dedicated server, returning stdout and stderr
// separately so callers can distinguish an absent server from a real failure.
func run(tmuxPath string, args ...string) (stdout, stderr []byte, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	serverArgs := append([]string{"-L", popupshell.SocketName, "-f", "/dev/null"}, args...)
	//nolint:gosec // tmuxPath comes from exec.LookPath for the fixed tmux binary name.
	cmd := exec.CommandContext(ctx, tmuxPath, serverArgs...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err = cmd.Run()
	if ctx.Err() != nil {
		err = fmt.Errorf("%w after %s", ctx.Err(), commandTimeout)
	}
	return outBuf.Bytes(), errBuf.Bytes(), err
}

// serverAbsent reports whether tmux failed because no server is running. macOS reports a missing
// socket as a connection error when the socket directory was never created; Linux and an existing
// directory report "no server running".
func serverAbsent(stderr string) bool {
	if strings.Contains(stderr, noServerMarker) {
		return true
	}
	return strings.Contains(stderr, noServerConnectionMarker) &&
		strings.Contains(stderr, missingSocketMarker)
}

func commandError(operation string, stderr []byte, err error) error {
	output := strings.TrimSpace(string(stderr))
	if output == "" {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return fmt.Errorf("%s: %s", operation, output)
}

// sanitizeText keeps each reported value on one line and never emits terminal control sequences.
func sanitizeText(value string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			return ' '
		case unicode.IsControl(r):
			return -1
		default:
			return r
		}
	}, value)
}
