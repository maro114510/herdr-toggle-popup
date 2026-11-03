package sessions

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"

	"github.com/maro114510/herdr-toggle-popup/internal/popupshell"
)

// Test list:
//
// - reset-current: removes only the saved shell of the current namespace and scope
// - reset-current: refuses an attached saved shell unless --force is given
// - reset-current: an incomplete context deletes nothing
// - reset-current: a missing target reports no saved shell and deletes nothing
// - reset-current: unexpected arguments are rejected before any tmux call

const (
	resetSocketPath            = "/tmp/herdr-reset-test.sock"
	resetSessionOther          = "herdr-toggle-popup-0000000000000000000000000000000a"
	resetSessionOtherNamespace = "herdr-toggle-popup-0000000000000000000000000000000b"
)

// resetEnvironment sets the context reset-current resolves its identity from.
func resetEnvironment(t *testing.T) {
	t.Helper()

	t.Setenv("HERDR_SOCKET_PATH", resetSocketPath)
	t.Setenv("HERDR_WORKSPACE_ID", "ws1")
	t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", `{"workspace_id":"ws1","focused_pane_cwd":"/focused/cwd"}`)
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", "")
}

// currentSession is the saved shell name the plugin resolves for resetEnvironment.
func currentSession(t *testing.T) string {
	t.Helper()

	identity, err := popupshell.ResolveIdentity("workspace", popupshell.DefaultEntrypoint)
	if err != nil {
		t.Fatalf("resolve identity: %v", err)
	}
	return identity.Session
}

func ownership(namespace, scope string) map[string]string {
	return map[string]string{
		"@herdr-namespace":  namespace,
		"@herdr-scope":      scope,
		"@herdr-entrypoint": "shell",
	}
}

func runResetCurrent(t *testing.T, args ...string) (int, string, string) {
	t.Helper()

	var stdout, stderr bytes.Buffer
	code := RunResetCurrent(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func serverSessionNames(t *testing.T) map[string]bool {
	t.Helper()

	//nolint:gosec // The test controls the tmux binary and every argument.
	cmd := exec.Command("tmux", "-L", sessionTestTmuxName, "-f", "/dev/null", "list-sessions", "-F", "#{session_name}")
	out, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "no server running") {
			return map[string]bool{}
		}
		t.Fatalf("list sessions: %v\n%s", err, out)
	}

	names := make(map[string]bool)
	for line := range strings.SplitSeq(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			names[line] = true
		}
	}
	return names
}

func assertSessionMissing(t *testing.T, name string) {
	t.Helper()

	if serverSessionNames(t)[name] {
		t.Errorf("session %q still exists, want it removed", name)
	}
}

func assertSessionPresent(t *testing.T, name string) {
	t.Helper()

	if !serverSessionNames(t)[name] {
		t.Errorf("session %q is missing, want it untouched", name)
	}
}

func TestRunResetCurrentRemovesOnlyTheCurrentContextSession(t *testing.T) {
	tmuxOnPath(t)
	isolatedServer(t)
	resetEnvironment(t)

	current := currentSession(t)
	newServerSession(t, current, ownership(namespaceAlpha, "workspace"))
	newServerSession(t, resetSessionOther, ownership(namespaceAlpha, "directory"))
	newServerSession(t, resetSessionOtherNamespace, ownership(namespaceBeta, "workspace"))

	code, stdout, stderr := runResetCurrent(t)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	if want := "reset-current: discarded saved shell " + current + "\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
	assertSessionMissing(t, current)
	assertSessionPresent(t, resetSessionOther)
	assertSessionPresent(t, resetSessionOtherNamespace)
}

func TestRunResetCurrentRefusesAnAttachedSessionWithoutForce(t *testing.T) {
	tmuxOnPath(t)
	isolatedServer(t)
	resetEnvironment(t)

	current := currentSession(t)
	newServerSession(t, current, ownership(namespaceAlpha, "workspace"))
	attachDetachedServerClient(t, current)

	code, stdout, stderr := runResetCurrent(t)

	if code == 0 {
		t.Fatal("exit code = 0, want non-zero while a client is attached")
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	for _, want := range []string{"attached", "--force"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, want it to mention %q", stderr, want)
		}
	}
	assertSessionPresent(t, current)

	code, stdout, stderr = runResetCurrent(t, "--force")

	if code != 0 {
		t.Fatalf("exit code = %d with --force, want 0; stderr=%q", code, stderr)
	}
	if want := "reset-current: discarded saved shell " + current + "\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
	assertSessionMissing(t, current)
}

func TestRunResetCurrentDeletesNothingWhenTheContextIsIncomplete(t *testing.T) {
	tmuxOnPath(t)
	isolatedServer(t)
	resetEnvironment(t)
	newServerSession(t, resetSessionOther, ownership(namespaceAlpha, "workspace"))
	t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", `{"workspace_id":"ws1"}`)

	code, stdout, stderr := runResetCurrent(t)

	if code == 0 {
		t.Fatal("exit code = 0, want non-zero for a missing focused cwd")
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "focused pane's cwd") {
		t.Errorf("stderr = %q, want the missing focused cwd cause", stderr)
	}
	assertSessionPresent(t, resetSessionOther)
}

func TestRunResetCurrentReportsAMissingTargetWithoutTouchingOtherSessions(t *testing.T) {
	tmuxOnPath(t)
	isolatedServer(t)
	resetEnvironment(t)
	newServerSession(t, resetSessionOther, ownership(namespaceAlpha, "workspace"))

	code, stdout, stderr := runResetCurrent(t)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if want := "reset-current: no saved shell for this context\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	assertSessionPresent(t, resetSessionOther)
}

func TestRunResetCurrentRejectsUnknownArguments(t *testing.T) {
	code, stdout, stderr := runResetCurrent(t, "--force", "extra")

	if code == 0 {
		t.Error("exit code = 0, want non-zero")
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	if want := "usage: toggle-popup reset-current [--force]\n"; stderr != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
}

// attachDetachedServerClient attaches a real PTY client to session and waits until tmux counts it.
func attachDetachedServerClient(t *testing.T, session string) {
	t.Helper()

	//nolint:gosec // The test controls the tmux binary and every argument.
	cmd := exec.Command("tmux", "-L", popupshell.SocketName, "-f", "/dev/null", "attach-session", "-t", session)
	cmd.Env = os.Environ()
	master, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 80, X: 0, Y: 0})
	if err != nil {
		t.Fatalf("start PTY tmux client: %v", err)
	}
	t.Cleanup(func() {
		_ = master.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	})
	go func() {
		_, _ = io.Copy(io.Discard, master)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if sessionClients(t, session) == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("session %q never reported an attached client", session)
}

func sessionClients(t *testing.T, session string) int {
	t.Helper()

	for line := range strings.SplitSeq(serverTmux(t, "list-sessions", "-F", "#{session_name}|#{session_attached}"), "\n") {
		name, count, ok := strings.Cut(strings.TrimSpace(line), "|")
		if !ok || name != session {
			continue
		}
		clients, err := strconv.Atoi(count)
		if err != nil {
			return 0
		}
		return clients
	}
	return 0
}
