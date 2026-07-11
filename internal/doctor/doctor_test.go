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

const testVersion = "0.2.1"

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
