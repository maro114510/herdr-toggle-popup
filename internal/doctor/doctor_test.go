package doctor

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Test list:
//
// Run
// - normal plugin environment reports herdr, tmux, plugin root, config, state, and version status
// - optional plugin environment values can be missing without crashing or returning non-zero
// - malformed config is reported without printing the file body
// - malformed state is reported without rewriting or backing it up
// - output never includes unrelated environment values, shell history, or config/state file bodies
// - unexpected arguments are rejected without echoing their values
// - a connected popup session reports identity, versions, effective settings, clipboard capabilities, and client state
// - an absent tmux server is reported without probing its options or clients
// - a missing Ms capability and unexpected clipboard settings are reported on separate lines
// - missing context and missing tools do not stop the remaining diagnosis
// - diagnosis issues only read-only tmux commands

const (
	testVersion = "0.2.1"

	// Identity fixtures for a workspace-scoped popup shell. The namespace and session name are
	// literals so the test does not recompute them with the code under test.
	diagnosticSocketPath = "/tmp/herdr-namespace-test.sock"
	diagnosticNamespace  = "397bb8158a3a718681ef7babf738463d"
	diagnosticSession    = "herdr-toggle-popup-21917e06389cbcc03679009926e14545"
	diagnosticContext    = `{"workspace_id":"ws1","focused_pane_cwd":"/focused/cwd"}`

	defaultConfig = "scope = \"workspace\"\n"
)

func writeExecutable(t *testing.T, dir, name string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	//nolint:gosec // Test helper intentionally creates executable fake commands for LookPath.
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func runDoctor(t *testing.T) (int, string, string) {
	t.Helper()

	var stdout, stderr bytes.Buffer
	code := Run(nil, &stdout, &stderr, testVersion)
	return code, stdout.String(), stderr.String()
}

func TestRun_ReportsNormalPluginEnvironment(t *testing.T) {
	binDir := t.TempDir()
	herdrPath := writeExecutable(t, binDir, "herdr-custom")
	writeExecutable(t, binDir, "tmux")
	t.Setenv("HERDR_BIN_PATH", herdrPath)
	t.Setenv("PATH", binDir)

	pluginRoot := t.TempDir()
	writeFile(t, pluginRoot, "herdr-plugin.toml", "version = \"0.2.1\"\n")
	t.Setenv("HERDR_PLUGIN_ROOT", pluginRoot)

	configDir := t.TempDir()
	writeFile(t, configDir, "config.toml", "scope = \"directory\"\npopup_size.shell = \"right:0.5:3\"\n")
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", configDir)

	stateDir := t.TempDir()
	writeFile(t, stateDir, "popups.json", `{"version":1,"popups":{}}`)
	t.Setenv("HERDR_PLUGIN_STATE_DIR", stateDir)

	code, stdout, stderr := runDoctor(t)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	for _, want := range []string{
		"version: ok",
		"herdr: ok",
		"tmux: ok",
		"plugin root: ok",
		"config: ok",
		"state: ok",
		"scope=directory",
		"entries=0",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want it to contain %q", stdout, want)
		}
	}
}

func TestRun_MissingOptionalEnvironmentReportsMissing(t *testing.T) {
	t.Setenv("HERDR_BIN_PATH", "")
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HERDR_PLUGIN_ROOT", "")
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", "")
	t.Setenv("HERDR_PLUGIN_STATE_DIR", "")

	code, stdout, stderr := runDoctor(t)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	for _, want := range []string{
		"herdr: missing",
		"tmux: missing",
		"plugin root: missing",
		"config: missing",
		"state: missing",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want it to contain %q", stdout, want)
		}
	}
}

func TestRun_DoesNotPrintSecretsOrFileBodies(t *testing.T) {
	binDir := t.TempDir()
	writeExecutable(t, binDir, "herdr")
	writeExecutable(t, binDir, "tmux")
	t.Setenv("HERDR_BIN_PATH", "")
	t.Setenv("PATH", binDir)
	t.Setenv("SECRET_TOKEN", "super-secret-token")
	t.Setenv("HISTFILE", "/tmp/history-with-private-command")

	pluginRoot := t.TempDir()
	writeFile(t, pluginRoot, "herdr-plugin.toml", "version = \"0.2.1\"\n")
	t.Setenv("HERDR_PLUGIN_ROOT", pluginRoot)

	configDir := t.TempDir()
	writeFile(t, configDir, "config.toml", "not valid toml {{{ super-secret-config-value\n")
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", configDir)

	stateDir := t.TempDir()
	statePath := writeFile(t, stateDir, "popups.json", `{"version":1,"popups":`)
	t.Setenv("HERDR_PLUGIN_STATE_DIR", stateDir)

	code, stdout, stderr := runDoctor(t)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	for _, want := range []string{
		"config: error",
		"state: error",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want it to contain %q", stdout, want)
		}
	}
	for _, unwanted := range []string{
		"super-secret-token",
		"history-with-private-command",
		"super-secret-config-value",
		`{"version":1,"popups":`,
	} {
		if strings.Contains(stdout, unwanted) {
			t.Errorf("stdout = %q, want it to not contain %q", stdout, unwanted)
		}
	}
	if _, err := os.Stat(statePath + ".bak"); err == nil {
		t.Fatalf("doctor created a state backup, but diagnostics must be non-mutating")
	}
	//nolint:gosec // statePath is created by this test under t.TempDir.
	if got, err := os.ReadFile(statePath); err != nil || string(got) != `{"version":1,"popups":` {
		t.Fatalf("state file changed: content=%q err=%v", string(got), err)
	}
}

func TestRun_UnexpectedArgumentsAreNotEchoed(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	code := Run([]string{"super-secret-argument"}, &stdout, &stderr, testVersion)

	if code == 0 {
		t.Errorf("exit code = 0, want non-zero")
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "unexpected arguments") {
		t.Errorf("stderr = %q, want unexpected arguments message", stderr.String())
	}
	if strings.Contains(stderr.String(), "super-secret-argument") {
		t.Errorf("stderr = %q, want it to not echo argument value", stderr.String())
	}
}

const fakeTmuxScript = `#!/bin/sh
printf '%s\n' "$*" >> "$FAKE_TMUX_LOG"
case "$*" in
  "-V") printf 'tmux 3.7c\n'; exit 0 ;;
esac
if [ "${FAKE_TMUX_SERVER:-present}" = "absent" ]; then
  case "$*" in
    *"list-sessions"*) printf 'no server running\n' >&2; exit 1 ;;
  esac
fi
case "$*" in
  *"list-sessions"*) printf '%s\n' "$FAKE_TMUX_SESSIONS"; exit 0 ;;
  *"list-clients"*) if [ -n "${FAKE_TMUX_CLIENTS:-}" ]; then printf '%s\n' "$FAKE_TMUX_CLIENTS"; fi; exit 0 ;;
  *"show-options -s set-clipboard"*) printf 'set-clipboard %s\n' "${FAKE_SET_CLIPBOARD:-external}"; exit 0 ;;
  *"show-options -w -t"*) printf 'window-size %s\n' "${FAKE_WINDOW_SIZE:-smallest}"; exit 0 ;;
  *"show-options -t"*"mouse"*) printf 'mouse %s\n' "${FAKE_MOUSE:-on}"; exit 0 ;;
  *"show-options -t"*"@herdr-close-key"*) printf '@herdr-close-key %s\n' "${FAKE_CLOSE_KEY:-M-l}"; exit 0 ;;
esac
exit 0
`

const fakeInfocmpScript = `#!/bin/sh
if [ "${FAKE_INFOCMP_FAIL:-}" = "1" ]; then
  exit 1
fi
if [ -n "${FAKE_MS_TERM:-}" ] && [ "$2" = "$FAKE_MS_TERM" ]; then
  printf 'Ms=\\E]52;%%p1%%s;%%p2%%s\\007,\n'
  exit 0
fi
printf 'colors#8,\n'
exit 0
`

const fakeHerdrScript = `#!/bin/sh
if [ "$1" = "--version" ]; then
  printf 'herdr 0.9.5\n'
fi
exit 0
`

// writeCommand writes an executable fake tool into dir.
func writeCommand(t *testing.T, dir, name, body string) {
	t.Helper()

	path := filepath.Join(dir, name)
	//nolint:gosec // Test helper intentionally creates executable fake commands for LookPath.
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
}

// setupPopupEnvironment creates a normal plugin environment whose tmux session and client data
// come from FAKE_TMUX_* variables the test sets afterwards. It returns the fake tmux call log.
func setupPopupEnvironment(t *testing.T, configContent string) string {
	t.Helper()

	binDir := t.TempDir()
	writeCommand(t, binDir, "herdr", fakeHerdrScript)
	writeCommand(t, binDir, "tmux", fakeTmuxScript)
	writeCommand(t, binDir, "infocmp", fakeInfocmpScript)
	logPath := filepath.Join(t.TempDir(), "tmux.log")
	t.Setenv("PATH", binDir)
	t.Setenv("FAKE_TMUX_LOG", logPath)
	t.Setenv("FAKE_TMUX_SERVER", "present")
	t.Setenv("FAKE_TMUX_SESSIONS", diagnosticSession+"\nother-session")
	t.Setenv("FAKE_TMUX_CLIENTS", diagnosticSession+"\t/dev/ttys003\txterm-256color\t120x40\tfocused")
	t.Setenv("FAKE_MS_TERM", "xterm-256color")
	t.Setenv("HERDR_BIN_PATH", "")
	t.Setenv("HERDR_SOCKET_PATH", diagnosticSocketPath)
	t.Setenv("HERDR_WORKSPACE_ID", "ws1")
	t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", diagnosticContext)
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("TERM_PROGRAM", "ghostty")

	pluginRoot := t.TempDir()
	writeFile(t, pluginRoot, "herdr-plugin.toml", "version = \"0.5.1\"\n")
	t.Setenv("HERDR_PLUGIN_ROOT", pluginRoot)

	configDir := t.TempDir()
	writeFile(t, configDir, "config.toml", configContent)
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", configDir)

	stateDir := t.TempDir()
	writeFile(t, stateDir, "popups.json", `{"version":1,"popups":{}}`)
	t.Setenv("HERDR_PLUGIN_STATE_DIR", stateDir)

	return logPath
}

//nolint:paralleltest // The test installs the plugin environment and fake tools process-wide.
func TestRun_ConnectedSessionReportsConnectionSettingsAndClipboard(t *testing.T) {
	logPath := setupPopupEnvironment(t, "scope = \"workspace\"\nwidth = \"80%\"\nheight = 20\nclose_key = \"M-l\"\npopup_size.shell = \"right:0.5:3\"\n")

	code, stdout, stderr := runDoctor(t)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	for _, want := range []string{
		"herdr version: ok (0.9.5)",
		"tmux version: ok (3.7c)",
		"config: ok (scope=workspace, popup_size entries=1)",
		"popup size: width=80%, height=20",
		"connection: ok (namespace=" + diagnosticNamespace + ", scope=workspace, session=" + diagnosticSession + ", entrypoint=shell)",
		"terminal: TERM=xterm-256color, TERM_PROGRAM=ghostty",
		"Ms: ok (TERM=xterm-256color)",
		"tmux server: present (sessions=2, clients=1)",
		"tmux session: present (clients=1)",
		"mouse: ok (on)",
		"set-clipboard: ok (external)",
		"window-size: ok (smallest)",
		"close key: ok (M-l)",
		"client: session=" + diagnosticSession + ", name=/dev/ttys003, term=xterm-256color, size=120x40, flags=focused, Ms=ok",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want it to contain %q", stdout, want)
		}
	}
	assertReadOnlyTmuxLog(t, logPath)
}

func TestRun_AbsentServerIsReportedWithoutProbingOptionsOrClients(t *testing.T) {
	logPath := setupPopupEnvironment(t, defaultConfig)
	t.Setenv("FAKE_TMUX_SERVER", "absent")

	code, stdout, stderr := runDoctor(t)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	for _, want := range []string{
		"tmux server: absent (not started by doctor)",
		"tmux session: unknown (server absent)",
		"set-clipboard: unknown (server absent)",
		"mouse: unknown (session not present)",
		"window-size: unknown (session not present)",
		"close key: unknown (configured M-l; session not present)",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want it to contain %q", stdout, want)
		}
	}
	for _, unwanted := range []string{"list-clients", "show-options"} {
		if calls := readFile(t, logPath); strings.Contains(calls, unwanted) {
			t.Errorf("tmux calls = %q, want no %q after an absent server", calls, unwanted)
		}
	}
}

func TestRun_MissingMsAndUnexpectedClipboardSettingsAreDistinct(t *testing.T) {
	setupPopupEnvironment(t, defaultConfig)
	t.Setenv("FAKE_MS_TERM", "")
	t.Setenv("FAKE_SET_CLIPBOARD", "on")
	t.Setenv("FAKE_MOUSE", "off")
	t.Setenv("FAKE_WINDOW_SIZE", "latest")

	code, stdout, stderr := runDoctor(t)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	for _, want := range []string{
		"Ms: missing (TERM=xterm-256color has no Ms; tmux cannot send OSC52 to the outer terminal)",
		"set-clipboard: warning (on; want external)",
		"mouse: warning (off; want on)",
		"window-size: warning (latest; want smallest)",
		"Ms=missing",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want it to contain %q", stdout, want)
		}
	}
}

func TestRun_MissingContextAndToolsStillDiagnosesConfiguredValues(t *testing.T) {
	binDir := t.TempDir()
	t.Setenv("PATH", binDir)
	t.Setenv("HERDR_BIN_PATH", "")
	t.Setenv("HERDR_PLUGIN_ROOT", "")
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", "")
	t.Setenv("HERDR_PLUGIN_STATE_DIR", "")
	t.Setenv("HERDR_SOCKET_PATH", "")
	t.Setenv("HERDR_WORKSPACE_ID", "")
	t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", "")
	t.Setenv("TERM", "")
	t.Setenv("TERM_PROGRAM", "")

	code, stdout, stderr := runDoctor(t)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	for _, want := range []string{
		"herdr: missing",
		"tmux: missing",
		"plugin root: missing",
		"config: missing",
		"popup size: width=100%, height=100%",
		"connection: warning (namespace=unknown, scope=workspace, session=unknown; HERDR_SOCKET_PATH must be set)",
		"terminal: TERM=unset, TERM_PROGRAM=unset",
		"Ms: unknown (TERM is not set)",
		"tmux server: unknown (tmux missing)",
		"state: missing",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want it to contain %q", stdout, want)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	//nolint:gosec // path is created by this test under t.TempDir.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// assertReadOnlyTmuxLog fails when doctor ran anything other than the read-only probes it needs.
func assertReadOnlyTmuxLog(t *testing.T, logPath string) {
	t.Helper()

	calls := readFile(t, logPath)
	if calls == "" {
		t.Fatal("tmux log is empty, want version and server probes")
	}
	for line := range strings.SplitSeq(strings.TrimSpace(calls), "\n") {
		switch {
		case line == "-V":
		case strings.HasPrefix(line, "-L herdr-toggle-popup -f /dev/null list-sessions"),
			strings.HasPrefix(line, "-L herdr-toggle-popup -f /dev/null list-clients"),
			strings.HasPrefix(line, "-L herdr-toggle-popup -f /dev/null show-options"):
		default:
			t.Errorf("doctor ran a non-read-only tmux command: %q", line)
		}
	}
}
