package popupshell

import (
	"bytes"
	"errors"
	"os"
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
// - tmux status line is disabled before attaching to the popup session
// - $SHELL unset: defaults the tmux command to /bin/zsh
// - missing tmux: reports a clear error and never execs
// - missing focused cwd: reports a clear error before execing tmux

const (
	configDirEnvVar = "HERDR_PLUGIN_CONFIG_DIR"
	contextEnvVar   = "HERDR_PLUGIN_CONTEXT_JSON"
	workspaceID     = "ws1"
	focusedCwd      = "/focused/cwd"
	testShellPath   = "/opt/homebrew/bin/fish"
	testShellBin    = "/resolved/sh"
	testTmuxBin     = "/resolved/tmux"
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

func TestRunDisablesTmuxStatusBeforeAttaching(t *testing.T) {
	t.Parallel()

	if !strings.Contains(tmuxAttachScript, "set-option -t \"$1\" status off") {
		t.Fatalf("tmux attach script = %q, want it to disable the target session status", tmuxAttachScript)
	}
	if !strings.Contains(tmuxAttachScript, "attach-session -t \"$1\"") {
		t.Fatalf("tmux attach script = %q, want it to attach after configuring the session", tmuxAttachScript)
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
