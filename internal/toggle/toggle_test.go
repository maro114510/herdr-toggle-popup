package toggle

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fullScreenPopupArgv = "plugin pane open --plugin maro114510.toggle-popup --entrypoint shell --placement popup --width 100% --height 100% --cwd /focused/cwd --focus\n"

func invoke(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	return Run(args, &stdout, &stderr), stderr.String()
}

func TestRunOpensFullScreenNativePopup(t *testing.T) {
	logPath := newFakeHerdr(t)
	t.Setenv("HERDR_PLUGIN_STATE_DIR", "")
	t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", `{"focused_pane_cwd":"/focused/cwd"}`)
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", "")

	code, stderr := invoke(t, "shell")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty when no config file exists", stderr)
	}
	log := readLog(t, logPath)
	want := fullScreenPopupArgv
	if log != want {
		t.Errorf("argv = %q, want %q", log, want)
	}
}

func TestRunAppliesConfiguredPopupDimensions(t *testing.T) {
	logPath := newFakeHerdr(t)
	configDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte("width = \"80%\"\nheight = 20\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", configDir)
	t.Setenv("HERDR_PLUGIN_STATE_DIR", "")
	t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", `{"focused_pane_cwd":"/focused/cwd"}`)
	t.Setenv("STUB_HERDR_EXPECT_WIDTH", "80%")
	t.Setenv("STUB_HERDR_EXPECT_HEIGHT", "20")

	code, stderr := invoke(t, "shell")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty for valid dimensions", stderr)
	}
	log := readLog(t, logPath)
	want := "plugin pane open --plugin maro114510.toggle-popup --entrypoint shell --placement popup --width 80% --height 20 --cwd /focused/cwd --focus\n"
	if log != want {
		t.Errorf("argv = %q, want %q", log, want)
	}
}

func TestRunWarnsAndFallsBackOnInvalidDimensions(t *testing.T) {
	logPath := newFakeHerdr(t)
	configDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte("width = \"wide\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", configDir)
	t.Setenv("HERDR_PLUGIN_STATE_DIR", "")
	t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", `{"focused_pane_cwd":"/focused/cwd"}`)

	code, stderr := invoke(t, "shell")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stderr, "invalid width") {
		t.Errorf("stderr = %q, want an invalid width warning", stderr)
	}
	log := readLog(t, logPath)
	want := fullScreenPopupArgv
	if log != want {
		t.Errorf("argv = %q, want the 100%% fallback, %q", log, want)
	}
}

func TestRunWarnsAndFallsBackOnMalformedConfig(t *testing.T) {
	logPath := newFakeHerdr(t)
	configDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte("scope = \"directory\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", configDir)
	t.Setenv("HERDR_PLUGIN_STATE_DIR", "")
	t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", `{"focused_pane_cwd":"/focused/cwd"}`)

	code, stderr := invoke(t, "shell")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stderr, "could not parse") || !strings.HasPrefix(stderr, "toggle: ") {
		t.Errorf("stderr = %q, want a toggle-prefixed config parse warning", stderr)
	}
	log := readLog(t, logPath)
	want := fullScreenPopupArgv
	if log != want {
		t.Errorf("argv = %q, want the default full-screen popup %q", log, want)
	}
}

func TestRunDoesNotInterpretPopupSizeAsDimensions(t *testing.T) {
	logPath := newFakeHerdr(t)
	configDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte("popup_size.shell = \"right:0.5:3\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", configDir)
	t.Setenv("HERDR_PLUGIN_STATE_DIR", "")
	t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", `{"focused_pane_cwd":"/focused/cwd"}`)

	code, stderr := invoke(t, "shell")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	log := readLog(t, logPath)
	want := fullScreenPopupArgv
	if log != want {
		t.Errorf("argv = %q, want %q", log, want)
	}
}

func TestRunRejectsMissingEntrypointWithoutCallingHerdr(t *testing.T) {
	logPath := newFakeHerdr(t)
	t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", `{"focused_pane_cwd":"/focused/cwd"}`)

	code, stderr := invoke(t)
	if code == 0 || !strings.Contains(stderr, "usage:") {
		t.Errorf("code = %d, stderr = %q, want usage failure", code, stderr)
	}
	log := readLog(t, logPath)
	if len(log) != 0 {
		t.Errorf("herdr log = %q, want no call", log)
	}
}

func TestRunRejectsMissingFocusedCwdWithoutCallingHerdr(t *testing.T) {
	logPath := newFakeHerdr(t)
	t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", `{}`)

	code, stderr := invoke(t, "shell")
	if code == 0 || !strings.Contains(stderr, "focused pane's cwd") {
		t.Errorf("code = %d, stderr = %q, want focused-cwd failure", code, stderr)
	}
	log := readLog(t, logPath)
	if len(log) != 0 {
		t.Errorf("herdr log = %q, want no call", log)
	}
}

func TestRunReportsNativePopupOpenFailure(t *testing.T) {
	newFakeHerdr(t)
	t.Setenv("HERDR_PLUGIN_STATE_DIR", "")
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", "")
	t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", `{"focused_pane_cwd":"/focused/cwd"}`)
	t.Setenv("STUB_HERDR_OPEN_EXIT", "1")

	code, stderr := invoke(t, "shell")
	if code == 0 || !strings.Contains(stderr, "stub open failure") {
		t.Errorf("code = %d, stderr = %q, want open failure", code, stderr)
	}
}

// TestRunIgnoresLegacyRegistryAndPreservesItsFile pins the v0.7.0 removal: a pre-native
// popups.json must not change how toggle behaves and must never be rewritten or deleted.
func TestRunIgnoresLegacyRegistryAndPreservesItsFile(t *testing.T) {
	logPath := newFakeHerdr(t)
	stateDir := t.TempDir()
	legacyRegistry := `{"version":1,"popups":{"workspace:ws1:shell":{"pane_id":"legacy-pane","plugin_id":"maro114510.toggle-popup","entrypoint":"shell","scope":"workspace","workspace_id":"ws1","tab_id":null,"created_at_unix_ms":0}}}` + "\n"
	statePath := filepath.Join(stateDir, "popups.json")
	if err := os.WriteFile(statePath, []byte(legacyRegistry), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_PLUGIN_STATE_DIR", stateDir)
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", "")
	t.Setenv("HERDR_WORKSPACE_ID", "")
	t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", `{"workspace_id":"ws1","focused_pane_cwd":"/focused/cwd"}`)

	code, stderr := invoke(t, "shell")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if got := readLog(t, logPath); got != fullScreenPopupArgv {
		t.Errorf("herdr calls = %q, want only the native popup open %q", got, fullScreenPopupArgv)
	}
	got, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("legacy registry was deleted: %v", err)
	}
	if string(got) != legacyRegistry {
		t.Errorf("legacy registry = %q, want it unchanged at %q", got, legacyRegistry)
	}
}
