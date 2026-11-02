package cleanup

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/maro114510/herdr-toggle-popup/internal/popupshell"
)

// Test list:
//
// - list reports managed candidates with their client state, sorted by session name
// - list reports an absent server as empty without starting one
// - list reports sessions without a usable ownership marker as unmanaged
// - list never deletes a detached session, however long it has existed
// - delete removes only the explicitly named managed sessions
// - delete rejects a non-plugin session
// - delete rejects a legacy session that has no ownership marker
// - delete rejects a session owned by another plugin
// - delete refuses an attached session without --force
// - delete --force removes an attached session
// - delete validates every target before deleting any
// - delete requires at least one target
// - an unknown action is rejected
// - real tmux server: list does not start an absent server
// - real tmux server: delete removes only the selected managed session
// - real tmux server: delete refuses an attached session and --force removes it

const (
	managedDetached = "herdr-toggle-popup-aaaa"
	managedAttached = "herdr-toggle-popup-bbbb"
	managedSibling  = "herdr-toggle-popup-eeee"
	legacySession   = "herdr-toggle-popup-cccc"
	otherOwned      = "herdr-toggle-popup-dddd"
	nonPlugin       = "unrelated-session"
	ownerValue      = "maro114510.toggle-popup"
	deletedOutput   = "deleted: 1\n"
	otherOwnerValue = "another.plugin"

	clientTimeout  = 5 * time.Second
	clientInterval = 25 * time.Millisecond
)

const fakeTmuxScript = `#!/bin/sh
printf '%s\n' "$*" >> "$FAKE_TMUX_LOG"
case "$5" in
  list-sessions)
    if [ "${FAKE_TMUX_SERVER:-present}" = "absent" ]; then
      printf 'no server running on /tmp/fake\n' >&2
      exit 1
    fi
    if [ -n "${FAKE_TMUX_SESSIONS:-}" ]; then
      printf '%s\n' "$FAKE_TMUX_SESSIONS"
    fi
    exit 0
    ;;
  kill-session)
    printf '%s\n' "$7" >> "$FAKE_TMUX_KILLED"
    exit 0
    ;;
esac
exit 0
`

// setupFakeTmux installs a fake tmux that answers list-sessions and kill-session from environment
// variables. It returns the call log and the list of targets kill-session was asked to delete.
func setupFakeTmux(t *testing.T) (logPath, killedPath string) {
	t.Helper()

	binDir := t.TempDir()
	tmuxPath := filepath.Join(binDir, "tmux")
	//nolint:gosec // The test intentionally installs an executable fake tmux.
	if err := os.WriteFile(tmuxPath, []byte(fakeTmuxScript), 0o700); err != nil {
		t.Fatal(err)
	}
	logPath = filepath.Join(t.TempDir(), "tmux.log")
	killedPath = filepath.Join(t.TempDir(), "killed.log")
	t.Setenv("PATH", binDir)
	t.Setenv("FAKE_TMUX_LOG", logPath)
	t.Setenv("FAKE_TMUX_KILLED", killedPath)
	t.Setenv("FAKE_TMUX_SERVER", "present")
	return logPath, killedPath
}

func runCleanup(t *testing.T, args ...string) (int, string, string) {
	t.Helper()

	var stdout, stderr bytes.Buffer
	code := Run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// sessionLine formats one list-sessions record the way the production format requests it.
func sessionLine(name string, clients int, owner string) string {
	return fmt.Sprintf("%s\t%d\t%s", name, clients, owner)
}

func allSessions() string {
	return strings.Join([]string{
		sessionLine(managedDetached, 0, ownerValue),
		sessionLine(managedAttached, 2, ownerValue),
		sessionLine(legacySession, 0, ""),
		sessionLine(otherOwned, 0, otherOwnerValue),
		sessionLine(nonPlugin, 0, ""),
	}, "\n")
}

func readOptionalFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatal(err)
	}
	return string(data)
}

func assertOnlyReadOnlyTmuxCalls(t *testing.T, logPath string) {
	t.Helper()

	for line := range strings.SplitSeq(strings.TrimSpace(readOptionalFile(t, logPath)), "\n") {
		if !strings.Contains(line, " list-sessions ") {
			t.Errorf("cleanup ran a mutating tmux command while listing: %q", line)
		}
	}
}

func TestRun_ListReportsManagedCandidatesWithClientState(t *testing.T) {
	logPath, killedPath := setupFakeTmux(t)
	t.Setenv("FAKE_TMUX_SESSIONS", allSessions())

	code, stdout, stderr := runCleanup(t, "list")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	want := "candidates: 2\n" +
		managedDetached + " clients=0 detached\n" +
		managedAttached + " clients=2 attached\n" +
		"unmanaged: 3\n"
	if stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
	if killed := readOptionalFile(t, killedPath); killed != "" {
		t.Errorf("kill-session targets = %q, want none", killed)
	}
	assertOnlyReadOnlyTmuxCalls(t, logPath)
}

func TestRun_ListReportsAnAbsentServerAsEmpty(t *testing.T) {
	logPath, killedPath := setupFakeTmux(t)
	t.Setenv("FAKE_TMUX_SERVER", "absent")

	code, stdout, stderr := runCleanup(t, "list")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if want := "candidates: 0\nunmanaged: 0\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
	if killed := readOptionalFile(t, killedPath); killed != "" {
		t.Errorf("kill-session targets = %q, want none", killed)
	}
	assertOnlyReadOnlyTmuxCalls(t, logPath)
}

func TestRun_ListDoesNotDeleteDetachedSessions(t *testing.T) {
	_, killedPath := setupFakeTmux(t)
	// A detached session with no clients is still only a candidate: nothing about its age or its
	// client count makes cleanup delete it.
	t.Setenv("FAKE_TMUX_SESSIONS", sessionLine(managedDetached, 0, ownerValue))

	code, stdout, _ := runCleanup(t, "list")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout, managedDetached+" clients=0 detached") {
		t.Errorf("stdout = %q, want the detached candidate", stdout)
	}
	if killed := readOptionalFile(t, killedPath); killed != "" {
		t.Errorf("kill-session targets = %q, want none", killed)
	}
}

func TestRun_DeleteRemovesOnlyTheNamedManagedSessions(t *testing.T) {
	_, killedPath := setupFakeTmux(t)
	t.Setenv("FAKE_TMUX_SESSIONS", allSessions())

	code, stdout, stderr := runCleanup(t, "delete", managedDetached)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	if stdout != deletedOutput {
		t.Errorf("stdout = %q, want %q", stdout, deletedOutput)
	}
	if want := "=" + managedDetached + "\n"; readOptionalFile(t, killedPath) != want {
		t.Errorf("kill-session targets = %q, want %q", readOptionalFile(t, killedPath), want)
	}
}

func TestRun_DeleteRejectsANonPluginSession(t *testing.T) {
	_, killedPath := setupFakeTmux(t)
	t.Setenv("FAKE_TMUX_SESSIONS", sessionLine(nonPlugin, 0, ownerValue))

	code, _, stderr := runCleanup(t, "delete", nonPlugin)

	if code == 0 {
		t.Errorf("exit code = 0, want non-zero")
	}
	if !strings.Contains(stderr, "not a plugin session") {
		t.Errorf("stderr = %q, want a non-plugin rejection", stderr)
	}
	if killed := readOptionalFile(t, killedPath); killed != "" {
		t.Errorf("kill-session targets = %q, want none", killed)
	}
}

func TestRun_DeleteRejectsALegacySession(t *testing.T) {
	_, killedPath := setupFakeTmux(t)
	t.Setenv("FAKE_TMUX_SESSIONS", sessionLine(legacySession, 0, ""))

	code, _, stderr := runCleanup(t, "delete", legacySession)

	if code == 0 {
		t.Errorf("exit code = 0, want non-zero")
	}
	if !strings.Contains(stderr, "legacy session without an ownership marker") {
		t.Errorf("stderr = %q, want a legacy rejection", stderr)
	}
	if killed := readOptionalFile(t, killedPath); killed != "" {
		t.Errorf("kill-session targets = %q, want none", killed)
	}
}

func TestRun_DeleteRejectsASessionOwnedByAnotherPlugin(t *testing.T) {
	_, killedPath := setupFakeTmux(t)
	t.Setenv("FAKE_TMUX_SESSIONS", sessionLine(otherOwned, 0, otherOwnerValue))

	code, _, stderr := runCleanup(t, "delete", otherOwned)

	if code == 0 {
		t.Errorf("exit code = 0, want non-zero")
	}
	if !strings.Contains(stderr, "ownership is unknown") {
		t.Errorf("stderr = %q, want an ownership rejection", stderr)
	}
	if killed := readOptionalFile(t, killedPath); killed != "" {
		t.Errorf("kill-session targets = %q, want none", killed)
	}
}

func TestRun_DeleteRefusesAnAttachedSessionWithoutForce(t *testing.T) {
	_, killedPath := setupFakeTmux(t)
	t.Setenv("FAKE_TMUX_SESSIONS", sessionLine(managedAttached, 2, ownerValue))

	code, _, stderr := runCleanup(t, "delete", managedAttached)

	if code == 0 {
		t.Errorf("exit code = 0, want non-zero")
	}
	if !strings.Contains(stderr, "attached to 2 client") {
		t.Errorf("stderr = %q, want an attached rejection", stderr)
	}
	if killed := readOptionalFile(t, killedPath); killed != "" {
		t.Errorf("kill-session targets = %q, want none", killed)
	}
}

func TestRun_DeleteForceRemovesAnAttachedSession(t *testing.T) {
	_, killedPath := setupFakeTmux(t)
	t.Setenv("FAKE_TMUX_SESSIONS", sessionLine(managedAttached, 2, ownerValue))

	code, stdout, stderr := runCleanup(t, "delete", "--force", managedAttached)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != deletedOutput {
		t.Errorf("stdout = %q, want %q", stdout, deletedOutput)
	}
	if want := "=" + managedAttached + "\n"; readOptionalFile(t, killedPath) != want {
		t.Errorf("kill-session targets = %q, want %q", readOptionalFile(t, killedPath), want)
	}
}

func TestRun_DeleteValidatesEveryTargetBeforeDeletingAny(t *testing.T) {
	_, killedPath := setupFakeTmux(t)
	t.Setenv("FAKE_TMUX_SESSIONS", allSessions())

	code, stdout, stderr := runCleanup(t, "delete", managedDetached, nonPlugin)

	if code == 0 {
		t.Errorf("exit code = 0, want non-zero")
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing deleted", stdout)
	}
	if !strings.Contains(stderr, "not a plugin session") {
		t.Errorf("stderr = %q, want a non-plugin rejection", stderr)
	}
	if killed := readOptionalFile(t, killedPath); killed != "" {
		t.Errorf("kill-session targets = %q, want none", killed)
	}
}

//nolint:paralleltest // The test changes the process-wide environment through its setup helper.
func TestRun_DeleteRequiresAtLeastOneTarget(t *testing.T) {
	logPath, _ := setupFakeTmux(t)

	code, _, stderr := runCleanup(t, "delete")

	if code == 0 {
		t.Errorf("exit code = 0, want non-zero")
	}
	if !strings.Contains(stderr, "usage:") {
		t.Errorf("stderr = %q, want usage", stderr)
	}
	if log := readOptionalFile(t, logPath); log != "" {
		t.Errorf("tmux calls = %q, want none before a target is named", log)
	}
}

//nolint:paralleltest // The test changes the process-wide environment through its setup helper.
func TestRun_UnknownActionIsRejected(t *testing.T) {
	logPath, _ := setupFakeTmux(t)

	code, _, stderr := runCleanup(t, "remove")

	if code == 0 {
		t.Errorf("exit code = 0, want non-zero")
	}
	if !strings.Contains(stderr, "unknown action") {
		t.Errorf("stderr = %q, want an unknown-action rejection", stderr)
	}
	if log := readOptionalFile(t, logPath); log != "" {
		t.Errorf("tmux calls = %q, want none", log)
	}
}

// realTmux resolves the tmux binary the real-server tests drive.
func realTmux(t *testing.T) string {
	t.Helper()

	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		t.Skipf("tmux was not found on PATH: %v", err)
	}
	return tmuxPath
}

// setupRealServer points the command at one isolated dedicated server and returns the socket path
// a server would create.
func setupRealServer(t *testing.T, tmuxPath string) string {
	t.Helper()

	// Keep the socket path short: tmux appends "/tmux-<uid>/<socket>" to a 104-byte sun_path limit.
	//nolint:usetesting // t.TempDir embeds the long test name and overflows sun_path.
	socketDir, err := os.MkdirTemp("", "htpclean")
	if err != nil {
		t.Fatalf("create tmux socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	t.Setenv("TMUX_TMPDIR", socketDir)
	t.Setenv("TMUX", "")
	t.Setenv("PATH", filepath.Dir(tmuxPath)+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Cleanup(func() {
		//nolint:gosec // The test controls the tmux binary and every argument.
		_ = exec.Command(tmuxPath, "-L", popupshell.SocketName, "-f", "/dev/null", "kill-server").Run()
	})
	return filepath.Join(socketDir, "tmux-"+strconv.Itoa(os.Getuid()), popupshell.SocketName)
}

func serverTmux(t *testing.T, tmuxPath string, args ...string) string {
	t.Helper()

	serverArgs := append([]string{"-L", popupshell.SocketName, "-f", "/dev/null"}, args...)
	//nolint:gosec // The test controls the tmux binary and every argument.
	cmd := exec.Command(tmuxPath, serverArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("tmux %v: %v\n%s", serverArgs, err, out)
	}
	return string(out)
}

func createSession(t *testing.T, tmuxPath, name, owner string) {
	t.Helper()

	serverTmux(t, tmuxPath, "new-session", "-d", "-s", name, "-c", t.TempDir(), "/bin/sh")
	if owner != "" {
		serverTmux(t, tmuxPath, "set-option", "-t", name, popupshell.OwnerOption, owner)
	}
}

func sessionExists(t *testing.T, tmuxPath, name string) bool {
	t.Helper()

	//nolint:gosec // The test controls the tmux binary and every argument.
	cmd := exec.Command(tmuxPath, "-L", popupshell.SocketName, "-f", "/dev/null", "has-session", "-t", "="+name)
	return cmd.Run() == nil
}

//nolint:paralleltest // The test changes the process-wide environment through its setup helper.
func TestRun_RealServerListDoesNotStartAnAbsentServer(t *testing.T) {
	tmuxPath := realTmux(t)
	socketPath := setupRealServer(t, tmuxPath)

	code, stdout, stderr := runCleanup(t, "list")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if want := "candidates: 0\nunmanaged: 0\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Errorf("server socket %q exists after listing (stat err = %v), want no server started", socketPath, err)
	}
}

//nolint:paralleltest // The test changes the process-wide environment through its setup helper.
func TestRun_RealServerDeleteRemovesOnlyTheSelectedManagedSession(t *testing.T) {
	tmuxPath := realTmux(t)
	setupRealServer(t, tmuxPath)

	createSession(t, tmuxPath, managedDetached, ownerValue)
	// managedSibling has no clients: its absence of clients must not make it a deletion target.
	createSession(t, tmuxPath, managedSibling, ownerValue)
	createSession(t, tmuxPath, legacySession, "")
	createSession(t, tmuxPath, otherOwned, otherOwnerValue)
	createSession(t, tmuxPath, nonPlugin, "")

	code, stdout, stderr := runCleanup(t, "delete", managedDetached)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != deletedOutput {
		t.Errorf("stdout = %q, want %q", stdout, deletedOutput)
	}
	if sessionExists(t, tmuxPath, managedDetached) {
		t.Errorf("session %q still exists, want it deleted", managedDetached)
	}
	for _, keep := range []string{managedSibling, legacySession, otherOwned, nonPlugin} {
		if !sessionExists(t, tmuxPath, keep) {
			t.Errorf("session %q was removed, want it kept", keep)
		}
	}
}

//nolint:paralleltest // The test changes the process-wide environment through its setup helper.
func TestRun_RealServerDeleteRefusesAnAttachedSession(t *testing.T) {
	tmuxPath := realTmux(t)
	setupRealServer(t, tmuxPath)

	createSession(t, tmuxPath, managedAttached, ownerValue)
	closeClient := startAttachedClient(t, tmuxPath, managedAttached)
	defer closeClient()

	code, _, stderr := runCleanup(t, "delete", managedAttached)

	if code == 0 {
		t.Errorf("exit code = 0, want non-zero")
	}
	if !strings.Contains(stderr, "attached") {
		t.Errorf("stderr = %q, want an attached rejection", stderr)
	}
	if !sessionExists(t, tmuxPath, managedAttached) {
		t.Errorf("session %q was removed, want it kept", managedAttached)
	}

	code, _, stderr = runCleanup(t, "delete", "--force", managedAttached)

	if code != 0 {
		t.Fatalf("forced exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if sessionExists(t, tmuxPath, managedAttached) {
		t.Errorf("session %q still exists, want forced deletion", managedAttached)
	}
}

// startAttachedClient attaches a real control-mode client and returns a function that ends it.
func startAttachedClient(t *testing.T, tmuxPath, session string) func() {
	t.Helper()

	stdinReader, stdinWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("create control client stdin: %v", err)
	}
	//nolint:gosec // The test controls the tmux binary and every argument.
	cmd := exec.Command(tmuxPath, "-L", popupshell.SocketName, "-f", "/dev/null", "-C", "attach-session", "-t", session)
	cmd.Stdin = stdinReader
	if err := cmd.Start(); err != nil {
		_ = stdinReader.Close()
		_ = stdinWriter.Close()
		t.Fatalf("start control client: %v", err)
	}
	_ = stdinReader.Close()

	closeClient := func() {
		_ = stdinWriter.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}
	t.Cleanup(closeClient)

	deadline := time.Now().Add(clientTimeout)
	for {
		out := serverTmux(t, tmuxPath, "list-clients", "-F", "#{client_session}")
		if strings.Contains(out, session) {
			return closeClient
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for a client attached to %s; clients=%q", session, out)
		}
		time.Sleep(clientInterval)
	}
}
