package doctor

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Test list:
//
// - real tmux server: diagnosis leaves sessions, options, and buffers unchanged
// - real tmux server: diagnosis does not start an absent server

const (
	popupSocketName  = "herdr-toggle-popup"
	minimumTmuxMajor = 2
	minimumTmuxMinor = 9
)

// realTmuxPath resolves a tmux that supports window-size smallest, skipping the real-server tests
// when the environment cannot run them.
func realTmuxPath(t *testing.T) string {
	t.Helper()

	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		t.Skipf("tmux was not found on PATH: %v", err)
	}
	//nolint:gosec // tmuxPath comes from exec.LookPath in the test.
	out, err := exec.Command(tmuxPath, "-V").Output()
	if err != nil {
		t.Fatalf("run %s -V: %v", tmuxPath, err)
	}
	var major, minor int
	if _, err := fmt.Sscanf(string(out), "tmux %d.%d", &major, &minor); err == nil {
		if major < minimumTmuxMajor || (major == minimumTmuxMajor && minor < minimumTmuxMinor) {
			t.Skipf("tmux %q is older than %d.%d", strings.TrimSpace(string(out)), minimumTmuxMajor, minimumTmuxMinor)
		}
	}
	return tmuxPath
}

// setupRealServerEnvironment points every diagnostic input at one isolated tmux server and returns
// the server's socket path. The fake herdr keeps version probing off the ambient install.
func setupRealServerEnvironment(t *testing.T, tmuxPath string) string {
	t.Helper()

	// Keep the socket path short: tmux appends "/tmux-<uid>/<socket>" to a 104-byte sun_path limit.
	//nolint:usetesting // t.TempDir embeds the long test name and overflows sun_path.
	socketDir, err := os.MkdirTemp("", "htpdoc")
	if err != nil {
		t.Fatalf("create tmux socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	t.Setenv("TMUX_TMPDIR", socketDir)
	t.Setenv("TMUX", "")

	fakeHerdrDir := t.TempDir()
	writeCommand(t, fakeHerdrDir, "herdr", fakeHerdrScript)
	t.Setenv("PATH", fakeHerdrDir+string(os.PathListSeparator)+filepath.Dir(tmuxPath)+string(os.PathListSeparator)+os.Getenv("PATH"))

	t.Setenv("HERDR_BIN_PATH", "")
	t.Setenv("HERDR_SOCKET_PATH", diagnosticSocketPath)
	t.Setenv("HERDR_WORKSPACE_ID", "ws1")
	t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", diagnosticContext)
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("TERM_PROGRAM", "htp-test")

	pluginRoot := t.TempDir()
	writeFile(t, pluginRoot, "herdr-plugin.toml", "version = \"0.5.1\"\n")
	t.Setenv("HERDR_PLUGIN_ROOT", pluginRoot)

	configDir := t.TempDir()
	writeFile(t, configDir, "config.toml", defaultConfig)
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", configDir)

	stateDir := t.TempDir()
	writeFile(t, stateDir, "popups.json", `{"version":1,"popups":{}}`)
	t.Setenv("HERDR_PLUGIN_STATE_DIR", stateDir)

	return filepath.Join(socketDir, "tmux-"+strconv.Itoa(os.Getuid()), popupSocketName)
}

// runServerTmux runs tmux against the doctor's dedicated server and fails on a non-zero exit.
func runServerTmux(t *testing.T, tmuxPath string, args ...string) string {
	t.Helper()

	serverArgs := append([]string{"-L", popupSocketName, "-f", "/dev/null"}, args...)
	//nolint:gosec // The test controls the tmux binary and every argument.
	cmd := exec.Command(tmuxPath, serverArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("tmux %v: %v\n%s", serverArgs, err, out)
	}
	return string(out)
}

// serverSnapshot records every piece of tmux state the diagnosis must leave alone.
func serverSnapshot(t *testing.T, tmuxPath string) string {
	t.Helper()

	return strings.Join([]string{
		runServerTmux(t, tmuxPath, "show-options", "-s", "set-clipboard"),
		runServerTmux(t, tmuxPath, "show-options", "-t", diagnosticSession, "mouse"),
		runServerTmux(t, tmuxPath, "show-options", "-w", "-t", diagnosticSession, "window-size"),
		runServerTmux(t, tmuxPath, "show-options", "-t", diagnosticSession, "@herdr-close-key"),
		runServerTmux(t, tmuxPath, "show-buffer"),
		runServerTmux(t, tmuxPath, "list-sessions", "-F", "#{session_name}"),
	}, "\n")
}

//nolint:paralleltest // The test installs the tmux and plugin environment process-wide.
func TestRun_RealServerDiagnosisLeavesSessionOptionsAndBufferUnchanged(t *testing.T) {
	tmuxPath := realTmuxPath(t)
	setupRealServerEnvironment(t, tmuxPath)
	t.Cleanup(func() {
		_ = exec.Command(tmuxPath, "-L", popupSocketName, "-f", "/dev/null", "kill-server").Run()
	})

	runServerTmux(t, tmuxPath, "new-session", "-d", "-s", diagnosticSession, "-c", t.TempDir(), "/bin/sh")
	runServerTmux(t, tmuxPath, "set-option", "-s", "set-clipboard", "external")
	runServerTmux(t, tmuxPath, "set-option", "-t", diagnosticSession, "mouse", "on")
	runServerTmux(t, tmuxPath, "set-option", "-w", "-t", diagnosticSession, "window-size", "smallest")
	runServerTmux(t, tmuxPath, "set-option", "-t", diagnosticSession, "@herdr-close-key", "M-l")
	runServerTmux(t, tmuxPath, "set-buffer", "herdr-doctor-sentinel")
	before := serverSnapshot(t, tmuxPath)

	var stdout, stderr bytes.Buffer
	code := Run(nil, &stdout, &stderr, testVersion)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	for _, want := range []string{
		"tmux server: present",
		"tmux session: present",
		"mouse: ok (on)",
		"set-clipboard: ok (external)",
		"window-size: ok (smallest)",
		"close key: ok (M-l)",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout = %q, want it to contain %q", stdout.String(), want)
		}
	}

	if after := serverSnapshot(t, tmuxPath); after != before {
		t.Errorf("tmux state changed after diagnosis:\nbefore=%q\nafter=%q", before, after)
	}
}

//nolint:paralleltest // The test installs the tmux and plugin environment process-wide.
func TestRun_RealServerDiagnosisDoesNotStartAnAbsentServer(t *testing.T) {
	tmuxPath := realTmuxPath(t)
	socketPath := setupRealServerEnvironment(t, tmuxPath)

	var stdout, stderr bytes.Buffer
	code := Run(nil, &stdout, &stderr, testVersion)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "tmux server: absent (not started by doctor)") {
		t.Errorf("stdout = %q, want an absent server report", stdout.String())
	}
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Errorf("server socket %q exists after diagnosis (stat err = %v), want no server started", socketPath, err)
	}
}
