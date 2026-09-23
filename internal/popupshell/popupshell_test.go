package popupshell

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Test list:
//
// - workspace scope: execs tmux new-session -A with a stable session name derived from
//   workspace:<workspace_id>:<entrypoint>, starting in the focused pane cwd
// - directory scope: derives the tmux session from directory:<focused cwd>:<entrypoint>
// - tab scope: derives the tmux session from tab:<workspace id>:<focused tab id>:<entrypoint>
// - tmux hides its status line, enables mouse mode, and binds Alt+L to detach on the dedicated server
// - real tmux server: a new popup session is created only on the dedicated server
// - real tmux server: a legacy same-name session on the default server is attached instead
// - real tmux server: the dedicated server keeps tmux's default mouse bindings and prefix
// - real tmux server: reopening reuses the existing dedicated session
// - $SHELL unset: defaults the tmux command to /bin/zsh
// - missing tmux: reports a clear error and never execs
// - missing focused cwd: reports a clear error before execing tmux
// - missing tab id (tab scope): reports a clear error before execing tmux

const (
	configDirEnvVar = "HERDR_PLUGIN_CONFIG_DIR"
	contextEnvVar   = "HERDR_PLUGIN_CONTEXT_JSON"
	workspaceID     = "ws1"
	focusedCwd      = "/focused/cwd"
	testShellPath   = "/opt/homebrew/bin/fish"
	testShellBin    = "/resolved/sh"
	testTmuxBin     = "/resolved/tmux"

	// Real-tmux-server test settings. popupSocketName mirrors the socket in tmuxAttachScript.
	popupSocketName  = "herdr-toggle-popup"
	tmuxSocketDirVar = "TMUX_TMPDIR"
	tmuxClientEnvVar = "TMUX"
	testSessionName  = "herdr-popup-test"
)

type execCall struct {
	argv0 string
	argv  []string
	envv  []string
}

func setupEnv(t *testing.T) string {
	t.Helper()

	configDir := filepath.Join(t.TempDir(), "plugin-config")
	t.Setenv(configDirEnvVar, configDir)
	t.Setenv(workspaceIDEnvVar, workspaceID)
	t.Setenv(contextEnvVar, `{"workspace_id":"ws1","focused_pane_cwd":"/focused/cwd"}`)
	t.Setenv(shellEnvVar, testShellPath)
	return configDir
}

func writeConfig(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func successfulLookPath(t *testing.T) lookPathFunc {
	t.Helper()
	return func(file string) (string, error) {
		switch file {
		case shellBin:
			return testShellBin, nil
		case tmuxBin:
			return testTmuxBin, nil
		case testShellPath:
			return testShellPath, nil
		case defaultShell:
			return defaultShell, nil
		default:
			t.Fatalf("unexpected lookPath(%q)", file)
			return "", nil
		}
	}
}

func wantTmuxWrapperArgv(session string, shell string) []string {
	return []string{
		shellBin, "-c", tmuxAttachScript,
		"popup-shell", session, focusedCwd, shell, testTmuxBin,
	}
}

func captureExec(call *execCall) execFunc {
	return func(argv0 string, argv, envv []string) error {
		call.argv0 = argv0
		call.argv = slices.Clone(argv)
		call.envv = slices.Clone(envv)
		return nil
	}
}

// isolatedTmuxEnv points tmux at a private socket directory and drops TMUX.
// The test therefore never touches an ambient server.
func isolatedTmuxEnv(t *testing.T) []string {
	t.Helper()

	// Keep the socket path short: tmux appends "/tmux-<uid>/default" to a 104-byte sun_path limit.
	//nolint:usetesting // t.TempDir embeds the long test name and overflows sun_path.
	dir, err := os.MkdirTemp("", "htp")
	if err != nil {
		t.Fatalf("create tmux socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	env := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, tmuxClientEnvVar+"=") ||
			strings.HasPrefix(entry, tmuxSocketDirVar+"=") {
			continue
		}
		env = append(env, entry)
	}
	return append(env, tmuxSocketDirVar+"="+dir)
}

// runTmux runs tmux against the isolated server and fails the test on a non-zero exit.
func runTmux(t *testing.T, env []string, tmuxPath string, args ...string) string {
	t.Helper()

	//nolint:gosec // The test controls the tmux binary and every argument.
	cmd := exec.Command(tmuxPath, args...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("tmux %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// runAttachScript runs the production attach script against the isolated servers. The script ends
// in `exec attach-session`, which cannot attach without a tty; only its side effects are asserted,
// so the resulting non-zero exit is expected and ignored.
func runAttachScript(t *testing.T, env []string, tmuxPath string) {
	t.Helper()

	//nolint:gosec // The test drives the production attach script with fixed arguments.
	cmd := exec.Command("sh", "-c", tmuxAttachScript, "popup-shell", testSessionName, t.TempDir(), "/bin/sh", tmuxPath)
	cmd.Env = env
	_ = cmd.Run()
}

// dedicatedTmux runs tmux against the plugin's dedicated server.
func dedicatedTmux(t *testing.T, env []string, tmuxPath string, args ...string) string {
	t.Helper()

	serverArgs := slices.Concat([]string{"-L", popupSocketName, "-f", "/dev/null"}, args)
	return runTmux(t, env, tmuxPath, serverArgs...)
}

// tmuxSessionExists reports whether the test session exists on the server selected by serverArgs.
func tmuxSessionExists(t *testing.T, env []string, tmuxPath string, serverArgs ...string) bool {
	t.Helper()

	args := append(slices.Clone(serverArgs), "has-session", "-t", testSessionName)
	//nolint:gosec // The test controls the tmux binary and every argument.
	cmd := exec.Command(tmuxPath, args...)
	cmd.Env = env
	return cmd.Run() == nil
}

// killTmuxServer tears down the server selected by serverArgs; a missing server is fine.
func killTmuxServer(t *testing.T, env []string, tmuxPath string, serverArgs ...string) {
	t.Helper()

	args := append(slices.Clone(serverArgs), "kill-server")
	//nolint:gosec // The test controls the tmux binary and every argument.
	cmd := exec.Command(tmuxPath, args...)
	cmd.Env = env
	_ = cmd.Run()
}

// isolatedServers tears down both isolated servers the attach script may touch.
func isolatedServers(t *testing.T, env []string, tmuxPath string) {
	t.Helper()

	t.Cleanup(func() {
		killTmuxServer(t, env, tmuxPath, "-L", popupSocketName, "-f", "/dev/null")
		killTmuxServer(t, env, tmuxPath, "-f", "/dev/null")
	})
}

func TestTmuxAttachScriptCreatesNewSessionOnlyOnDedicatedServer(t *testing.T) {
	t.Parallel()

	tmuxPath, err := exec.LookPath(tmuxBin)
	if err != nil {
		t.Skip("tmux not found, skipping real tmux server test")
	}
	env := isolatedTmuxEnv(t)
	isolatedServers(t, env, tmuxPath)

	runAttachScript(t, env, tmuxPath)

	if !tmuxSessionExists(t, env, tmuxPath, "-L", popupSocketName, "-f", "/dev/null") {
		t.Errorf("session %q was not created on the dedicated server", testSessionName)
	}
	if tmuxSessionExists(t, env, tmuxPath, "-f", "/dev/null") {
		t.Errorf("session %q exists on the default server, want it only on the dedicated server", testSessionName)
	}
}

func TestTmuxAttachScriptAttachesToLegacySessionOnDefaultServer(t *testing.T) {
	t.Parallel()

	tmuxPath, err := exec.LookPath(tmuxBin)
	if err != nil {
		t.Skip("tmux not found, skipping real tmux server test")
	}
	env := isolatedTmuxEnv(t)
	isolatedServers(t, env, tmuxPath)

	runTmux(t, env, tmuxPath, "-f", "/dev/null", "new-session", "-d", "-s", testSessionName, "-c", t.TempDir(), "/bin/sh")
	runAttachScript(t, env, tmuxPath)

	if !tmuxSessionExists(t, env, tmuxPath, "-f", "/dev/null") {
		t.Errorf("legacy session %q on the default server is gone", testSessionName)
	}
	if tmuxSessionExists(t, env, tmuxPath, "-L", popupSocketName, "-f", "/dev/null") {
		t.Error("a dedicated session was created despite a legacy session being available")
	}
}

func TestTmuxAttachScriptKeepsDefaultBindingsOnDedicatedServer(t *testing.T) {
	t.Parallel()

	tmuxPath, err := exec.LookPath(tmuxBin)
	if err != nil {
		t.Skip("tmux not found, skipping real tmux server test")
	}
	env := isolatedTmuxEnv(t)
	isolatedServers(t, env, tmuxPath)

	runAttachScript(t, env, tmuxPath)

	if got := strings.TrimSpace(dedicatedTmux(t, env, tmuxPath, "show-options", "-t", testSessionName, "status")); got != "status off" {
		t.Errorf("session status = %q, want %q", got, "status off")
	}
	if got := strings.TrimSpace(dedicatedTmux(t, env, tmuxPath, "show-options", "-t", testSessionName, "mouse")); got != "mouse on" {
		t.Errorf("session mouse = %q, want %q", got, "mouse on")
	}

	keys := dedicatedTmux(t, env, tmuxPath, "list-keys", "-T", "root")
	for _, want := range []string{"WheelUpPane", "MouseDrag1Pane", "M-l", "detach-client"} {
		if !strings.Contains(keys, want) {
			t.Errorf("dedicated root key table is missing %q:\n%s", want, keys)
		}
	}
	if got := strings.TrimSpace(dedicatedTmux(t, env, tmuxPath, "show-options", "-g", "prefix")); got != "prefix C-b" {
		t.Errorf("dedicated prefix = %q, want %q", got, "prefix C-b")
	}
}

func TestTmuxAttachScriptReusesExistingDedicatedSession(t *testing.T) {
	t.Parallel()

	tmuxPath, err := exec.LookPath(tmuxBin)
	if err != nil {
		t.Skip("tmux not found, skipping real tmux server test")
	}
	env := isolatedTmuxEnv(t)
	isolatedServers(t, env, tmuxPath)

	runAttachScript(t, env, tmuxPath)
	created := strings.TrimSpace(dedicatedTmux(t, env, tmuxPath, "display", "-p", "-t", testSessionName, "#{session_created}"))

	runAttachScript(t, env, tmuxPath)
	again := strings.TrimSpace(dedicatedTmux(t, env, tmuxPath, "display", "-p", "-t", testSessionName, "#{session_created}"))

	if created == "" || created != again {
		t.Errorf("session_created changed from %q to %q, want the existing shell session preserved", created, again)
	}
}

//nolint:paralleltest // uses setupEnv, which mutates process env via t.Setenv.
func TestRunExecsTmuxSessionForWorkspaceScope(t *testing.T) {
	setupEnv(t)

	var stderr bytes.Buffer
	var call execCall
	code := run([]string{defaultEntrypoint}, &stderr, successfulLookPath(t), captureExec(&call))

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	wantArgv := wantTmuxWrapperArgv(sessionName("workspace:ws1:shell"), testShellPath)
	if call.argv0 != testShellBin {
		t.Errorf("argv0 = %q, want %q", call.argv0, testShellBin)
	}
	if !slices.Equal(call.argv, wantArgv) {
		t.Errorf("argv = %v, want %v", call.argv, wantArgv)
	}
	if !slices.Equal(call.envv, os.Environ()) {
		t.Error("envv was not the inherited environment")
	}
}

func TestRunReadsWorkspaceIDFromPluginContextForNativePopup(t *testing.T) {
	setupEnv(t)
	t.Setenv(workspaceIDEnvVar, "")

	var stderr bytes.Buffer
	var call execCall
	code := run([]string{defaultEntrypoint}, &stderr, successfulLookPath(t), captureExec(&call))

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if got := call.argv[4]; got != sessionName("workspace:ws1:shell") {
		t.Errorf("session = %q, want workspace session derived from plugin context", got)
	}
}

//nolint:paralleltest // uses setupEnv, which mutates process env via t.Setenv.
func TestRunExecsTmuxSessionForDirectoryScope(t *testing.T) {
	configDir := setupEnv(t)
	writeConfig(t, configDir, "scope = \"directory\"\n")

	var stderr bytes.Buffer
	var call execCall
	code := run([]string{defaultEntrypoint}, &stderr, successfulLookPath(t), captureExec(&call))

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	wantSession := sessionName("directory:/focused/cwd:shell")
	if got := call.argv[4]; got != wantSession {
		t.Errorf("session = %q, want %q", got, wantSession)
	}
}

//nolint:paralleltest // uses setupEnv, which mutates process env via t.Setenv.
func TestRunInvalidScopePrintsWarningAndFallsBackToWorkspace(t *testing.T) {
	configDir := setupEnv(t)
	writeConfig(t, configDir, "scope = \"bogus-scope\"\n")

	var stderr bytes.Buffer
	var call execCall
	code := run([]string{defaultEntrypoint}, &stderr, successfulLookPath(t), captureExec(&call))

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "invalid scope") || !strings.Contains(stderr.String(), "bogus-scope") {
		t.Errorf("stderr = %q, want it to contain an invalid scope warning mentioning bogus-scope", stderr.String())
	}
	wantSession := sessionName("workspace:ws1:shell")
	if got := call.argv[4]; got != wantSession {
		t.Errorf("session = %q, want %q (fallback to workspace scope)", got, wantSession)
	}
}

//nolint:paralleltest // uses setupEnv, which mutates process env via t.Setenv.
func TestRunValidScopePrintsNoWarning(t *testing.T) {
	configDir := setupEnv(t)
	writeConfig(t, configDir, "scope = \"directory\"\n")

	var stderr bytes.Buffer
	var call execCall
	code := run([]string{defaultEntrypoint}, &stderr, successfulLookPath(t), captureExec(&call))

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "invalid scope") {
		t.Errorf("stderr = %q, want no invalid scope warning", stderr.String())
	}
}

//nolint:paralleltest // uses setupEnv, which mutates process env via t.Setenv.
func TestRunUnsetScopePrintsNoWarning(t *testing.T) {
	setupEnv(t)

	var stderr bytes.Buffer
	var call execCall
	code := run([]string{defaultEntrypoint}, &stderr, successfulLookPath(t), captureExec(&call))

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "invalid scope") {
		t.Errorf("stderr = %q, want no invalid scope warning", stderr.String())
	}
}

func TestTmuxSessionKeyTable(t *testing.T) {
	const cwdOnlyContext = `{"focused_pane_cwd":"/focused/cwd"}`

	tests := []struct {
		name      string
		scopeMode string
		context   string
		wantKey   string
		wantErr   bool
	}{
		{
			name:      "workspace",
			scopeMode: "workspace",
			context:   cwdOnlyContext,
			wantKey:   "workspace:ws1:shell",
			wantErr:   false,
		},
		{
			name:      "directory",
			scopeMode: "directory",
			context:   cwdOnlyContext,
			wantKey:   "directory:/focused/cwd:shell",
			wantErr:   false,
		},
		{
			name:      "tab",
			scopeMode: scopeTab,
			context:   `{"focused_pane_cwd":"/focused/cwd","tab_id":"tab1"}`,
			wantKey:   "tab:ws1:tab1:shell",
			wantErr:   false,
		},
		{
			name:      "tab missing tab_id",
			scopeMode: scopeTab,
			context:   cwdOnlyContext,
			wantKey:   "",
			wantErr:   true,
		},
	}

	t.Setenv(workspaceIDEnvVar, workspaceID)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(contextEnvVar, tt.context)
			key, _, err := tmuxSessionKey(tt.scopeMode, defaultEntrypoint)
			if tt.wantErr {
				if err == nil {
					t.Fatal("err = nil, want an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if key != tt.wantKey {
				t.Errorf("key = %q, want %q", key, tt.wantKey)
			}
		})
	}
}

func TestRunExecsTmuxSessionForTabScope(t *testing.T) {
	configDir := setupEnv(t)
	writeConfig(t, configDir, "scope = \"tab\"\n")
	t.Setenv(contextEnvVar, `{"workspace_id":"ws1","focused_pane_cwd":"/focused/cwd","tab_id":"tab1"}`)

	var stderr bytes.Buffer
	var call execCall
	code := run([]string{defaultEntrypoint}, &stderr, successfulLookPath(t), captureExec(&call))

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	wantSession := sessionName("tab:ws1:tab1:shell")
	if got := call.argv[4]; got != wantSession {
		t.Errorf("session = %q, want %q", got, wantSession)
	}
}

func TestRunMissingTabIDReportsErrorBeforeExec(t *testing.T) {
	configDir := setupEnv(t)
	writeConfig(t, configDir, "scope = \"tab\"\n")
	t.Setenv(contextEnvVar, `{"workspace_id":"ws1","focused_pane_cwd":"/focused/cwd"}`)

	var stderr bytes.Buffer
	execCalled := false
	code := run([]string{defaultEntrypoint}, &stderr, successfulLookPath(t), func(string, []string, []string) error {
		execCalled = true
		return nil
	})

	if code == 0 {
		t.Fatal("exit code = 0, want non-zero")
	}
	if execCalled {
		t.Error("exec was called despite missing tab id")
	}
	if stderr.Len() == 0 {
		t.Error("stderr is empty, want an error message")
	}
}

func TestRunConfiguresDedicatedTmuxServerBeforeAttaching(t *testing.T) {
	t.Parallel()

	for _, want := range []string{
		"-L herdr-toggle-popup",
		"has-session -t \"$1\"",
		"new-session -d -s \"$1\" -c \"$2\" \"$3\"",
		"set-option -t \"$1\" status off",
		"bind-key -n M-l detach-client",
		"set-option -t \"$1\" mouse on",
		"attach-session -t \"$1\"",
	} {
		if !strings.Contains(tmuxAttachScript, want) {
			t.Fatalf("tmux attach script = %q, want it to contain %q", tmuxAttachScript, want)
		}
	}
}

func TestRunEnablesTmuxMouseModeBeforeAttaching(t *testing.T) {
	t.Parallel()

	if !strings.Contains(tmuxAttachScript, "set-option -t \"$1\" mouse on") {
		t.Fatalf("tmux attach script = %q, want it to enable mouse mode on the target session", tmuxAttachScript)
	}
}

func TestRunAttachScriptReliesOnDefaultKeyTable(t *testing.T) {
	t.Parallel()

	if strings.Contains(tmuxAttachScript, "key-table") {
		t.Fatalf("tmux attach script = %q, want tmux default bindings instead of a custom key table", tmuxAttachScript)
	}
}

func TestRunDefaultsToZshWhenShellUnset(t *testing.T) {
	setupEnv(t)
	t.Setenv(shellEnvVar, "")

	var stderr bytes.Buffer
	var call execCall
	code := run([]string{defaultEntrypoint}, &stderr, successfulLookPath(t), captureExec(&call))

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if got := call.argv[6]; got != defaultShell {
		t.Errorf("shell argv = %q, want %q", got, defaultShell)
	}
}

//nolint:paralleltest // uses setupEnv, which mutates process env via t.Setenv.
func TestRunTmuxLookPathFailureReportsErrorAndNeverExecs(t *testing.T) {
	setupEnv(t)

	var stderr bytes.Buffer
	execCalled := false
	lookPath := func(file string) (string, error) {
		if file == tmuxBin {
			return "", errors.New("tmux not found")
		}
		return file, nil
	}
	execProcess := func(string, []string, []string) error {
		execCalled = true
		return nil
	}

	code := run([]string{defaultEntrypoint}, &stderr, lookPath, execProcess)

	if code == 0 {
		t.Fatal("exit code = 0, want non-zero")
	}
	if execCalled {
		t.Error("exec was called despite a tmux lookup failure")
	}
	if stderr.Len() == 0 {
		t.Error("stderr is empty, want an error message")
	}
}

//nolint:paralleltest // uses setupEnv, which mutates process env via t.Setenv.
func TestRunExecFailureReportsError(t *testing.T) {
	setupEnv(t)

	var stderr bytes.Buffer
	code := run([]string{defaultEntrypoint}, &stderr, successfulLookPath(t), func(string, []string, []string) error {
		return errors.New("exec failed")
	})

	if code == 0 {
		t.Fatal("exit code = 0, want non-zero")
	}
	if stderr.Len() == 0 {
		t.Error("stderr is empty, want an error message")
	}
}

func TestRunMissingFocusedCwdReportsErrorBeforeExec(t *testing.T) {
	setupEnv(t)
	t.Setenv(contextEnvVar, `{"workspace_id":"ws1"}`)

	var stderr bytes.Buffer
	execCalled := false
	code := run([]string{defaultEntrypoint}, &stderr, successfulLookPath(t), func(string, []string, []string) error {
		execCalled = true
		return nil
	})

	if code == 0 {
		t.Fatal("exit code = 0, want non-zero")
	}
	if execCalled {
		t.Error("exec was called despite missing focused cwd")
	}
	if stderr.Len() == 0 {
		t.Error("stderr is empty, want an error message")
	}
}
