package onpaneclosed

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/maro114510/herdr-toggle-popup/internal/state"
)

// Test list (ported from tests/on-pane-closed.bats):
//
// - HERDR_PANE_ID unset or empty: exits 0 without ever touching the registry
//   (os.Getenv makes unset and empty indistinguishable, so one test covers both)
// - deletes the registry entry whose pane_id matches HERDR_PANE_ID
// - leaves entries with a different pane_id untouched
// - HERDR_PANE_ID matches nothing registered: exits 0, registry unchanged
// - HERDR_PLUGIN_STATE_DIR unset while HERDR_PANE_ID is set: errors

const (
	stateDirEnvVar = "HERDR_PLUGIN_STATE_DIR"

	keyWs1 = "workspace:ws1:shell"
	keyWs2 = "workspace:ws2:shell"

	paneWs1 = "pane-1"
	paneWs2 = "pane-2"
)

func setEntry(t *testing.T, store *state.Store, key, paneID string) {
	t.Helper()

	entry := state.Entry{
		PaneID:          paneID,
		PluginID:        "maro114510.toggle-popup",
		Entrypoint:      "shell",
		Scope:           "workspace",
		WorkspaceID:     nil,
		TabID:           nil,
		CreatedAtUnixMs: 1,
		Hidden:          nil,
	}
	if err := store.Set(key, entry); err != nil {
		t.Fatal(err)
	}
}

func TestPaneIDEmptyExitsZeroWithoutTouchingRegistry(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "plugin-state")
	t.Setenv(stateDirEnvVar, stateDir)
	t.Setenv(paneIDEnvVar, "")
	store := state.NewStore(stateDir)
	setEntry(t, store, keyWs1, paneWs1)

	var stdout, stderr bytes.Buffer
	code := Run(nil, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if _, ok, err := store.Get(keyWs1); err != nil || !ok {
		t.Errorf("entry %q missing or errored: ok=%v err=%v", keyWs1, ok, err)
	}
}

func TestDeletesEntryMatchingPaneID(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "plugin-state")
	t.Setenv(stateDirEnvVar, stateDir)
	t.Setenv(paneIDEnvVar, paneWs1)
	store := state.NewStore(stateDir)
	setEntry(t, store, keyWs1, paneWs1)

	var stdout, stderr bytes.Buffer
	code := Run(nil, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if _, ok, err := store.Get(keyWs1); err != nil || ok {
		t.Errorf("entry %q still present: ok=%v err=%v", keyWs1, ok, err)
	}
}

func TestLeavesHiddenEntryMatchingPaneIDUntouched(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "plugin-state")
	t.Setenv(stateDirEnvVar, stateDir)
	t.Setenv(paneIDEnvVar, paneWs1)
	store := state.NewStore(stateDir)
	setEntry(t, store, keyWs1, paneWs1)
	if err := store.SetHidden(keyWs1, true); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run(nil, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	entry, ok, err := store.Get(keyWs1)
	if err != nil || !ok {
		t.Fatalf("entry %q missing or errored: ok=%v err=%v", keyWs1, ok, err)
	}
	if entry.Hidden == nil || !*entry.Hidden {
		t.Errorf("hidden = %v, want true", entry.Hidden)
	}
}

func TestLeavesEntriesWithDifferentPaneIDUntouched(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "plugin-state")
	t.Setenv(stateDirEnvVar, stateDir)
	t.Setenv(paneIDEnvVar, paneWs1)
	store := state.NewStore(stateDir)
	setEntry(t, store, keyWs1, paneWs1)
	setEntry(t, store, keyWs2, paneWs2)

	var stdout, stderr bytes.Buffer
	code := Run(nil, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	entry, ok, err := store.Get(keyWs2)
	if err != nil || !ok {
		t.Fatalf("entry %q missing or errored: ok=%v err=%v", keyWs2, ok, err)
	}
	if entry.PaneID != paneWs2 {
		t.Errorf("pane_id = %q, want %q", entry.PaneID, paneWs2)
	}
}

func TestPaneIDMatchingNothingLeavesRegistryUnchanged(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "plugin-state")
	t.Setenv(stateDirEnvVar, stateDir)
	t.Setenv(paneIDEnvVar, "pane-unrelated")
	store := state.NewStore(stateDir)
	setEntry(t, store, keyWs1, paneWs1)

	var stdout, stderr bytes.Buffer
	code := Run(nil, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if _, ok, err := store.Get(keyWs1); err != nil || !ok {
		t.Errorf("entry %q missing or errored: ok=%v err=%v", keyWs1, ok, err)
	}
}

func TestMissingStateDirWithPaneIDSetErrors(t *testing.T) {
	t.Setenv(stateDirEnvVar, "")
	t.Setenv(paneIDEnvVar, paneWs1)

	var stdout, stderr bytes.Buffer
	code := Run(nil, &stdout, &stderr)

	if code == 0 {
		t.Errorf("exit code = 0, want non-zero")
	}
	if stderr.Len() == 0 {
		t.Errorf("stderr is empty, want an error message")
	}
}
