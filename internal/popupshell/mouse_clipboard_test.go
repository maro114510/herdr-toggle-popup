package popupshell

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

// Test list:
//
// - real tmux + PTY: a mouse drag fills the tmux buffer and emits OSC52 carrying the same text
// - real tmux + PTY: a terminal without Ms fills the tmux buffer but emits no OSC52

const (
	// clipboardTerm is the terminal the PTY client presents to tmux. Its terminfo carries the Ms
	// capability, which tmux needs before it can send a selection to the outer terminal.
	clipboardTerm = "xterm-256color"
	// noMsTerm is a terminal that supports mouse reporting but has no Ms capability.
	noMsTerm = "screen"

	dragTarget       = "HTP-CLIPBOARD-DRAG"
	ptyClientCols    = 80
	ptyClientRows    = 24
	dragEventTimeout = 10 * time.Second
	dragStepDelay    = 50 * time.Millisecond
)

// lockedBuffer collects PTY output safely while the test reads snapshots from another goroutine.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// ptyTmuxClient is one real terminal client attached to the popup session. Unlike the control-mode
// clients used by the sizing tests, it receives mouse input and renders tmux's output, so it can
// observe the OSC52 sequence tmux sends to an outer terminal.
type ptyTmuxClient struct {
	master *os.File
	cmd    *exec.Cmd
	output *lockedBuffer
	closed bool
}

func (c *ptyTmuxClient) close(t *testing.T) {
	t.Helper()
	if c.closed {
		return
	}
	c.closed = true
	_ = c.master.Close()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	_ = c.cmd.Wait()
}

// startPtyTmuxClient attaches a real PTY client with TERM=term and records everything tmux writes
// back, including OSC52 sequences addressed to the outer terminal.
func startPtyTmuxClient(t *testing.T, env []string, tmuxPath, term string) *ptyTmuxClient {
	t.Helper()

	cmd := exec.Command(tmuxPath, "-L", popupSocketName, "-f", "/dev/null", "attach-session", "-t", testSessionName)
	cmd.Env = envWithTerm(env, term)
	master, err := pty.StartWithSize(cmd, &pty.Winsize{
		Rows: ptyClientRows,
		Cols: ptyClientCols,
		X:    0,
		Y:    0,
	})
	if err != nil {
		t.Fatalf("start PTY tmux client: %v", err)
	}

	client := &ptyTmuxClient{master: master, cmd: cmd, output: new(lockedBuffer), closed: false}
	t.Cleanup(func() { client.close(t) })

	go func() {
		_, _ = io.Copy(client.output, master)
	}()

	return client
}

// envWithTerm replaces TERM so the PTY client presents exactly the requested terminal.
func envWithTerm(env []string, term string) []string {
	filtered := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if !strings.HasPrefix(entry, "TERM=") {
			filtered = append(filtered, entry)
		}
	}
	return append(filtered, "TERM="+term)
}

// terminalHasMs reports whether the host terminfo entry for term declares the Ms capability.
// The test uses it to prove that a no-OSC52 result comes from a terminal that truly lacks Ms,
// instead of reporting clipboard support that was never exercised.
func terminalHasMs(t *testing.T, term string) bool {
	t.Helper()

	//nolint:gosec // The test controls the term name and infocmp is a read-only lookup.
	out, err := exec.Command("infocmp", "-x", term).Output()
	if err != nil {
		t.Fatalf("read terminfo for %q: %v", term, err)
	}
	for field := range strings.SplitSeq(string(out), ",") {
		if strings.HasPrefix(strings.TrimSpace(field), "Ms=") {
			return true
		}
	}
	return false
}

// waitForPtyText waits until the PTY client has received want.
func waitForPtyText(t *testing.T, client *ptyTmuxClient, want string) {
	t.Helper()

	eventually(t, fmt.Sprintf("PTY output %q", want), func() (bool, string) {
		got := client.output.String()
		return strings.Contains(got, want), fmt.Sprintf("PTY output length %d", len(got))
	})
}

// sendMouseDrag drags the left button across the first row from column 1 to column end.
// SGR mouse reporting is the encoding tmux enables for clients with mouse mode on.
// Several motion events are sent because tmux 2.9 uses the first one to start the drag from the
// press position and only later ones to extend the selection.
func sendMouseDrag(t *testing.T, client *ptyTmuxClient, end int) {
	t.Helper()

	writeMouse := func(button, x int, final byte) {
		t.Helper()
		if _, err := fmt.Fprintf(client.master, "\x1b[<%d;%d;1%c", button, x, final); err != nil {
			t.Fatalf("write mouse event: %v", err)
		}
		time.Sleep(dragStepDelay)
	}

	writeMouse(0, 1, 'M') // press on the first cell
	for _, x := range dragMotionColumns(end) {
		writeMouse(32, x, 'M') // move with the left button held
	}
	writeMouse(0, end, 'm') // release on the last cell
}

// dragMotionColumns spans the first row so every supported tmux version sees at least one motion
// event after the one that starts the drag.
func dragMotionColumns(end int) []int {
	columns := make([]int, 0, 3)
	for _, x := range []int{2, end / 2, end} {
		if x > 1 && (len(columns) == 0 || x != columns[len(columns)-1]) {
			columns = append(columns, x)
		}
	}
	return columns
}

// waitForTmuxBuffer waits until the server holds want as its most recent buffer.
func waitForTmuxBuffer(t *testing.T, env []string, tmuxPath, want string) {
	t.Helper()

	eventually(t, fmt.Sprintf("tmux buffer %q", want), func() (bool, string) {
		got, ok := tmuxShowBuffer(env, tmuxPath)
		if !ok {
			return false, "no tmux buffer"
		}
		return got == want, fmt.Sprintf("tmux buffer = %q", got)
	})
}

// tmuxShowBuffer returns the most recent buffer and whether one exists.
func tmuxShowBuffer(env []string, tmuxPath string) (string, bool) {
	cmd := exec.Command(tmuxPath, "-L", popupSocketName, "-f", "/dev/null", "show-buffer")
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return strings.TrimRight(string(out), "\n"), true
}

// osc52Payloads decodes every OSC52 clipboard sequence in output.
func osc52Payloads(t *testing.T, output string) []string {
	t.Helper()

	pattern := regexp.MustCompile(`\x1b\]52;[^;]*;([A-Za-z0-9+/=]+)(?:\x07|\x1b\\)`)
	matches := pattern.FindAllStringSubmatch(output, -1)
	payloads := make([]string, 0, len(matches))
	for _, match := range matches {
		decoded, err := base64.StdEncoding.DecodeString(match[1])
		if err != nil {
			t.Fatalf("decode OSC52 payload %q: %v", match[1], err)
		}
		payloads = append(payloads, string(decoded))
	}
	return payloads
}

// waitForOSC52 waits until the PTY client has received an OSC52 payload equal to want.
func waitForOSC52(t *testing.T, client *ptyTmuxClient, want string) {
	t.Helper()

	deadline := time.Now().Add(dragEventTimeout)
	for {
		if slices.Contains(osc52Payloads(t, client.output.String()), want) {
			return
		}
		if time.Now().After(deadline) {
			payloads := osc52Payloads(t, client.output.String())
			t.Fatalf("no OSC52 payload %q; tmux emitted %q. A terminal without Ms cannot reach the outer clipboard", want, payloads)
		}
		time.Sleep(dragStepDelay)
	}
}

// runDragSetup creates the saved popup session, gives its pane a predictable first line, and
// attaches a real PTY client with term. It returns the client once the target text is rendered.
func runDragSetup(t *testing.T, env []string, tmuxPath, term string) *ptyTmuxClient {
	t.Helper()

	runAttachScript(t, env, tmuxPath, testSessionName, testCloseKey)
	dedicatedTmux(t, env, tmuxPath, "respawn-pane", "-k", "-t", testSessionName,
		fmt.Sprintf("printf '%s\\n'; sleep 60", dragTarget))
	waitForPaneText(t, env, tmuxPath, testSessionName, dragTarget)

	client := startPtyTmuxClient(t, env, tmuxPath, term)
	waitForPtyText(t, client, dragTarget)
	return client
}

// TestTmuxAttachScriptMouseDragCopiesToOuterClipboard drives the complete path a user takes:
// a real mouse drag in a real PTY client, the tmux buffer at drag end, and the OSC52 sequence
// tmux writes to that client. Asserting only the bindings would let a missing Ms or a broken
// copy path pass, so both final results are read back here.
func TestTmuxAttachScriptMouseDragCopiesToOuterClipboard(t *testing.T) {
	t.Parallel()

	tmuxPath := realTmux(t)
	env := isolatedTmuxEnv(t)
	isolatedServers(t, env, tmuxPath)

	if !terminalHasMs(t, clipboardTerm) {
		t.Fatalf("terminfo for %q has no Ms capability, so clipboard support cannot be verified", clipboardTerm)
	}

	client := runDragSetup(t, env, tmuxPath, clipboardTerm)
	sendMouseDrag(t, client, len(dragTarget))

	waitForTmuxBuffer(t, env, tmuxPath, dragTarget)
	waitForOSC52(t, client, dragTarget)
}

// TestTmuxAttachScriptMouseDragWithoutMsSkipsOSC52 keeps the clipboard claim honest: selection
// still fills the tmux buffer, but with no Ms capability tmux must not emit OSC52, so the test
// cannot report that the outer clipboard was updated.
func TestTmuxAttachScriptMouseDragWithoutMsSkipsOSC52(t *testing.T) {
	t.Parallel()

	tmuxPath := realTmux(t)
	env := isolatedTmuxEnv(t)
	isolatedServers(t, env, tmuxPath)

	if terminalHasMs(t, noMsTerm) {
		t.Fatalf("terminfo for %q gained Ms, so it no longer proves the no-Ms path", noMsTerm)
	}

	client := runDragSetup(t, env, tmuxPath, noMsTerm)
	sendMouseDrag(t, client, len(dragTarget))

	waitForTmuxBuffer(t, env, tmuxPath, dragTarget)
	// The buffer appears on release, but tmux writes to the client asynchronously. Give the PTY
	// reader time to collect the follow-up output so an OSC52 sent just after the copy is seen.
	time.Sleep(2 * dragStepDelay)
	if payloads := osc52Payloads(t, client.output.String()); len(payloads) != 0 {
		t.Fatalf("terminal without Ms emitted OSC52 %q, want none", payloads)
	}
}
