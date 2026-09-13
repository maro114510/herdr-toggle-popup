package toggle

import (
	"bytes"
	"strings"
	"testing"

	"github.com/maro114510/herdr-toggle-popup/internal/state"
)

func invoke(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	return Run(args, &stdout, &stderr), stderr.String()
}

func TestRunOpensFullScreenNativePopup(t *testing.T) {
	logPath := newFakeHerdr(t)
	t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", `{"focused_pane_cwd":"/focused/cwd"}`)

	code, stderr := invoke(t, "shell")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	log := readLog(t, logPath)
	want := "plugin pane open --plugin maro114510.toggle-popup --entrypoint shell --placement popup --width 100% --height 100% --cwd /focused/cwd --focus\n"
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
	t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", `{"focused_pane_cwd":"/focused/cwd"}`)
	t.Setenv("STUB_HERDR_OPEN_EXIT", "1")

	code, stderr := invoke(t, "shell")
	if code == 0 || !strings.Contains(stderr, "stub open failure") {
		t.Errorf("code = %d, stderr = %q, want open failure", code, stderr)
	}
}

func TestRunClosesVisibleLegacyOverlayBeforeOpeningNativePopup(t *testing.T) {
	logPath := newFakeHerdr(t)
	stateDir := t.TempDir()
	t.Setenv("HERDR_PLUGIN_STATE_DIR", stateDir)
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", "")
	t.Setenv("HERDR_WORKSPACE_ID", "")
	t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", `{"workspace_id":"ws1","focused_pane_cwd":"/focused/cwd"}`)
	store := state.NewStore(stateDir)
	workspaceID := "ws1"
	if err := store.Set("workspace:ws1:shell", state.Entry{
		PaneID:          "legacy-pane",
		PluginID:        pluginID,
		Entrypoint:      "shell",
		Scope:           "workspace",
		WorkspaceID:     &workspaceID,
		TabID:           nil,
		CreatedAtUnixMs: 0,
		Hidden:          nil,
	}); err != nil {
		t.Fatal(err)
	}

	code, stderr := invoke(t, "shell")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if got := readLog(t, logPath); got != "pane get legacy-pane\nplugin pane close legacy-pane\n" {
		t.Errorf("herdr calls = %q, want legacy pane close only", got)
	}
	entry, found, err := store.Get("workspace:ws1:shell")
	if err != nil {
		t.Fatal(err)
	}
	if !found || entry.Hidden == nil || !*entry.Hidden {
		t.Errorf("legacy entry = %+v, found = %v, want hidden", entry, found)
	}
}
