package popupshell

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// Test list (ported from tests/popup-shell.bats, plus the argv/env-plumbing this Go port adds
// its own seam for):
//
// - resolves $SHELL when set, execs it with argv=[shell] and the inherited environment
// - defaults to /bin/zsh when $SHELL is unset
// - a lookup failure (shell not found) is reported to stderr, exec is never attempted
// - an exec failure is reported to stderr, non-zero exit
// - end-to-end: execs $SHELL, replacing the process (same PID, no Go parent remains)
// - end-to-end: execs /bin/zsh when $SHELL is unset

const helperProcessEnvVar = "POPUPSHELL_TEST_HELPER_PROCESS"

func TestRunResolvesShellFromEnv(t *testing.T) {
	t.Setenv(shellEnvVar, "/opt/homebrew/bin/fish")

	var stderr bytes.Buffer
	var gotArgv0 string
	var gotArgv, gotEnvv []string

	lookPath := func(file string) (string, error) {
		if file != "/opt/homebrew/bin/fish" {
			t.Errorf("lookPath called with %q, want %q", file, "/opt/homebrew/bin/fish")
		}
		return "/resolved/fish", nil
	}
	exec := func(argv0 string, argv, envv []string) error {
		gotArgv0, gotArgv, gotEnvv = argv0, argv, envv
		return nil
	}

	code := run(&stderr, lookPath, exec)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if gotArgv0 != "/resolved/fish" {
		t.Errorf("argv0 = %q, want %q", gotArgv0, "/resolved/fish")
	}
	if want := []string{"/opt/homebrew/bin/fish"}; !slices.Equal(gotArgv, want) {
		t.Errorf("argv = %v, want %v", gotArgv, want)
	}
	if !slices.Equal(gotEnvv, os.Environ()) {
		t.Errorf("envv was not the inherited environment")
	}
}

func TestRunDefaultsToZshWhenShellUnset(t *testing.T) {
	t.Setenv(shellEnvVar, "")

	var stderr bytes.Buffer
	var gotFile string

	lookPath := func(file string) (string, error) {
		gotFile = file
		return defaultShell, nil
	}
	exec := func(string, []string, []string) error { return nil }

	code := run(&stderr, lookPath, exec)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if gotFile != defaultShell {
		t.Errorf("lookPath called with %q, want %q", gotFile, defaultShell)
	}
}

func TestRunLookPathFailureReportsErrorAndNeverExecs(t *testing.T) {
	t.Setenv(shellEnvVar, "/no/such/shell")

	var stderr bytes.Buffer
	execCalled := false

	lookPath := func(string) (string, error) { return "", errors.New("no such file or directory") }
	exec := func(string, []string, []string) error {
		execCalled = true
		return nil
	}

	code := run(&stderr, lookPath, exec)

	if code == 0 {
		t.Errorf("exit code = 0, want non-zero")
	}
	if execCalled {
		t.Error("exec was called despite a lookPath failure")
	}
	if stderr.Len() == 0 {
		t.Error("stderr is empty, want an error message")
	}
}

func TestRunExecFailureReportsError(t *testing.T) {
	t.Setenv(shellEnvVar, "/bin/zsh")

	var stderr bytes.Buffer

	lookPath := func(file string) (string, error) { return file, nil }
	exec := func(string, []string, []string) error { return errors.New("permission denied") }

	code := run(&stderr, lookPath, exec)

	if code == 0 {
		t.Errorf("exit code = 0, want non-zero")
	}
	if stderr.Len() == 0 {
		t.Error("stderr is empty, want an error message")
	}
}

// TestRunReplacesProcessWithFixtureShell re-execs the test binary as a helper process with
// SHELL pointing at a stub script, the same way tests/popup-shell.bats proves the process was
// replaced in place: the stub writes its own PID to a file, which must equal the PID of the
// process this test spawned — proof that popup-shell.Run() became the stub rather than forking
// a child under a surviving Go parent.
func TestRunReplacesProcessWithFixtureShell(t *testing.T) {
	t.Parallel()

	if os.Getenv(helperProcessEnvVar) == "1" {
		os.Exit(Run(nil, os.Stdout, os.Stderr))
	}

	dir := t.TempDir()
	stub := filepath.Join(dir, "stub-shell")
	pidFile := filepath.Join(dir, "pid")
	script := "#!/usr/bin/env bash\necho $$ > " + strconv.Quote(pidFile) + "\n"
	if err := os.WriteFile(stub, []byte(script), 0o700); err != nil { //nolint:gosec // test fixture must be executable.
		t.Fatal(err)
	}

	//nolint:gosec // re-execs this same test binary (os.Args[0]) with a fixed flag, the standard Go self-exec test pattern.
	cmd := exec.Command(os.Args[0], "-test.run=^TestRunReplacesProcessWithFixtureShell$")
	cmd.Env = append(os.Environ(), helperProcessEnvVar+"=1", shellEnvVar+"="+stub)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("helper process failed: %v (stderr: %s)", err, stderr.String())
	}

	gotPID, err := os.ReadFile(pidFile) //nolint:gosec // pidFile is a path this test built under t.TempDir().
	if err != nil {
		t.Fatalf("reading pid file: %v", err)
	}
	if want := strconv.Itoa(cmd.Process.Pid); strings.TrimSpace(string(gotPID)) != want {
		t.Errorf("stub ran with pid %s, want %s (the spawned process's own pid)", strings.TrimSpace(string(gotPID)), want)
	}
}

// TestRunExecsRealZshWhenShellUnset mirrors tests/popup-shell.bats' "execs /bin/zsh when SHELL
// is unset" case.
func TestRunExecsRealZshWhenShellUnset(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath(defaultShell); err != nil {
		t.Skip("/bin/zsh not available on this system")
	}

	if os.Getenv(helperProcessEnvVar) == "1" {
		os.Exit(Run(nil, os.Stdout, os.Stderr))
	}

	//nolint:gosec // re-execs this same test binary (os.Args[0]) with a fixed flag, the standard Go self-exec test pattern.
	cmd := exec.Command(os.Args[0], "-test.run=^TestRunExecsRealZshWhenShellUnset$")
	cmd.Env = append(os.Environ(), helperProcessEnvVar+"=1")
	cmd.Env = removeEnvVar(cmd.Env, shellEnvVar)
	cmd.Stdin = strings.NewReader("echo ZSHV=$ZSH_VERSION\n")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("helper process failed: %v (stderr: %s)", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "ZSHV=") {
		t.Errorf("stdout = %q, want it to contain %q", stdout.String(), "ZSHV=")
	}
}

func removeEnvVar(env []string, key string) []string {
	prefix := key + "="
	filtered := env[:0]
	for _, kv := range env {
		if !strings.HasPrefix(kv, prefix) {
			filtered = append(filtered, kv)
		}
	}
	return filtered
}
