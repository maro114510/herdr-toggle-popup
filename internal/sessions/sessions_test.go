package sessions

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/maro114510/herdr-toggle-popup/internal/popupshell"
)

// Test list:
//
// - sessions: lists each plugin session with its namespace, scope, entrypoint, and client count
// - sessions: reports a plugin session without ownership metadata as ownership-unknown
// - sessions: omits a non-plugin session from the listing
// - sessions: an absent server reports no sessions and creates no server
// - sessions: a server with no plugin sessions reports none
// - sessions: unexpected arguments are rejected without touching tmux

const (
	requireTmuxEnvVar = "HERDR_TEST_REQUIRE_TMUX"

	sessionOwnedFirst   = "herdr-toggle-popup-00000000000000000000000000000001"
	sessionOwnedSecond  = "herdr-toggle-popup-00000000000000000000000000000002"
	sessionLegacy       = "herdr-toggle-popup-00000000000000000000000000000003"
	sessionNonPlugin    = "notes-00000000000000000000000000000000"
	namespaceAlpha      = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	namespaceBeta       = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	sessionTestTmuxName = popupshell.SocketName
)

// tmuxOnPath resolves tmux for the real-server tests, skipping unless the environment requires
// them, as the popupshell real-server tests do.
func tmuxOnPath(t *testing.T) string {
	t.Helper()

	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		if os.Getenv(requireTmuxEnvVar) == "1" {
			t.Fatalf("%s=1, but tmux was not found on PATH: %v", requireTmuxEnvVar, err)
		}
		t.Skipf("tmux was not found on PATH: %v", err)
	}
	return tmuxPath
}

// isolatedServer points tmux at a private socket directory and drops TMUX, so the test never
// touches an ambient server.
func isolatedServer(t *testing.T) {
	t.Helper()

	// Keep the socket path short: tmux appends "/tmux-<uid>/<socket>" to a 104-byte sun_path limit.
	//nolint:usetesting // t.TempDir embeds the long test name and overflows sun_path.
	dir, err := os.MkdirTemp("", "htps")
	if err != nil {
		t.Fatalf("create tmux socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("TMUX_TMPDIR", dir)
	t.Setenv("TMUX", "")
	t.Cleanup(func() {
		//nolint:gosec // The test controls the tmux binary and every argument.
		_ = exec.Command("tmux", "-L", sessionTestTmuxName, "-f", "/dev/null", "kill-server").Run()
	})
}

// newServerSession creates a detached session on the dedicated server and fails the test when
// tmux rejects it.
func newServerSession(t *testing.T, name string, options map[string]string) {
	t.Helper()

	serverTmux(t, "new-session", "-d", "-s", name, "-c", t.TempDir(), "/bin/sh")
	for option, value := range options {
		serverTmux(t, "set-option", "-t", name, option, value)
	}
}

// serverTmux runs tmux against the dedicated server and fails the test on a non-zero exit.
func serverTmux(t *testing.T, args ...string) string {
	t.Helper()

	serverArgs := append([]string{"-L", sessionTestTmuxName, "-f", "/dev/null"}, args...)
	//nolint:gosec // The test controls the tmux binary and every argument.
	cmd := exec.Command("tmux", serverArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("tmux %v: %v\n%s", serverArgs, err, out)
	}
	return string(out)
}

func runList(t *testing.T, args ...string) (int, string, string) {
	t.Helper()

	var stdout, stderr bytes.Buffer
	code := RunList(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestRunListShowsOwnershipContext(t *testing.T) {
	tmuxOnPath(t)
	isolatedServer(t)

	newServerSession(t, sessionOwnedFirst, map[string]string{
		"@herdr-namespace":  namespaceAlpha,
		"@herdr-scope":      "workspace",
		"@herdr-entrypoint": "shell",
	})
	newServerSession(t, sessionOwnedSecond, map[string]string{
		"@herdr-namespace":  namespaceBeta,
		"@herdr-scope":      "directory",
		"@herdr-entrypoint": "shell",
	})
	newServerSession(t, sessionLegacy, nil)
	newServerSession(t, sessionNonPlugin, nil)

	code, stdout, stderr := runList(t)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}

	want := strings.Join([]string{
		"sessions: 3",
		"session: name=" + sessionOwnedFirst + ", namespace=" + namespaceAlpha + ", scope=workspace, entrypoint=shell, clients=0",
		"session: name=" + sessionOwnedSecond + ", namespace=" + namespaceBeta + ", scope=directory, entrypoint=shell, clients=0",
		"session: name=" + sessionLegacy + ", ownership=unknown, clients=0",
		"",
	}, "\n")
	if stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
}

func TestRunListReportsAbsentServerWithoutStartingOne(t *testing.T) {
	tmuxOnPath(t)
	isolatedServer(t)
	socketPath := filepath.Join(os.Getenv("TMUX_TMPDIR"), "tmux-"+strconv.Itoa(os.Getuid()), sessionTestTmuxName)

	code, stdout, stderr := runList(t)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	if want := "sessions: none (tmux server absent)\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Errorf("server socket %q exists after listing (stat err = %v), want no server started", socketPath, err)
	}
}

func TestRunListReportsNoPluginSessionsOnServerWithOnlyNonPluginSessions(t *testing.T) {
	tmuxOnPath(t)
	isolatedServer(t)
	newServerSession(t, sessionNonPlugin, nil)

	code, stdout, stderr := runList(t)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if want := "sessions: none\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
}

func TestRunListRejectsUnexpectedArguments(t *testing.T) {
	code, stdout, stderr := runList(t, "extra")

	if code == 0 {
		t.Errorf("exit code = 0, want non-zero")
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	if want := "usage: toggle-popup sessions\n"; stderr != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
}
