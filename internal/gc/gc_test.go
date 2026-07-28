package gc

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maro114510/herdr-toggle-popup/internal/state"
)

// Test list:
//
// - HERDR_PLUGIN_STATE_DIR unset: exits non-zero with an error, never calls herdr
// - empty registry: exits 0, reports 0 entries removed
// - visible entry whose pane no longer exists: removed
// - visible entry whose pane exists: left unchanged
// - hidden entry whose pane no longer exists: left unchanged (its pane was already closed on
//   hide by design; toggle.go reopens it by tmux session name, not by that pane id, so gc must
//   not treat it as orphaned)
// - mixed entries: only the orphaned visible entry is removed, count reported reflects that

const (
	stateDirEnvVar = "HERDR_PLUGIN_STATE_DIR"

	keyLive    = "workspace:ws1:shell"
	keyOrphan  = "workspace:ws1:logs"
	keyHidden  = "workspace:ws1:notes"
	paneLive   = "pane-live"
	paneOrphan = "pane-orphan"
	paneHidden = "pane-hidden"
)

func setEntry(t *testing.T, store *state.Store, key, paneID string, hidden bool) {
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
	if hidden {
		if err := store.SetHidden(key, true); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMissingStateDirErrors(t *testing.T) {
	t.Setenv(stateDirEnvVar, "")

	var stdout, stderr bytes.Buffer
	code := Run(nil, &stdout, &stderr)

	if code == 0 {
		t.Errorf("exit code = 0, want non-zero")
	}
	if stderr.Len() == 0 {
		t.Errorf("stderr is empty, want an error message")
	}
}

func TestEmptyRegistryExitsZeroReportsZeroRemoved(t *testing.T) {
	newFakeHerdr(t)
	stateDir := filepath.Join(t.TempDir(), "plugin-state")
	t.Setenv(stateDirEnvVar, stateDir)

	var stdout, stderr bytes.Buffer
	code := Run(nil, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
	if !strings.Contains(stdout.String(), "0") {
		t.Errorf("stdout = %q, want it to report 0 entries removed", stdout.String())
	}
}

func TestVisibleEntryWithNonexistentPaneIsRemoved(t *testing.T) {
	newFakeHerdr(t)
	t.Setenv("STUB_HERDR_MISSING_PANE_IDS", paneOrphan)
	stateDir := filepath.Join(t.TempDir(), "plugin-state")
	t.Setenv(stateDirEnvVar, stateDir)
	store := state.NewStore(stateDir)
	setEntry(t, store, keyOrphan, paneOrphan, false)

	var stdout, stderr bytes.Buffer
	code := Run(nil, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if _, ok, err := store.Get(keyOrphan); err != nil || ok {
		t.Errorf("entry %q still present: ok=%v err=%v", keyOrphan, ok, err)
	}
	if !strings.Contains(stdout.String(), "1") {
		t.Errorf("stdout = %q, want it to report 1 entry removed", stdout.String())
	}
}

func TestVisibleEntryWithExistingPaneIsLeftUnchanged(t *testing.T) {
	newFakeHerdr(t)
	stateDir := filepath.Join(t.TempDir(), "plugin-state")
	t.Setenv(stateDirEnvVar, stateDir)
	store := state.NewStore(stateDir)
	setEntry(t, store, keyLive, paneLive, false)

	var stdout, stderr bytes.Buffer
	code := Run(nil, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if _, ok, err := store.Get(keyLive); err != nil || !ok {
		t.Errorf("entry %q missing or errored: ok=%v err=%v", keyLive, ok, err)
	}
}

func TestHiddenEntryWithNonexistentPaneIsLeftUnchanged(t *testing.T) {
	newFakeHerdr(t)
	t.Setenv("STUB_HERDR_MISSING_PANE_IDS", paneHidden)
	stateDir := filepath.Join(t.TempDir(), "plugin-state")
	t.Setenv(stateDirEnvVar, stateDir)
	store := state.NewStore(stateDir)
	setEntry(t, store, keyHidden, paneHidden, true)

	var stdout, stderr bytes.Buffer
	code := Run(nil, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	entry, ok, err := store.Get(keyHidden)
	if err != nil || !ok {
		t.Fatalf("entry %q missing or errored: ok=%v err=%v", keyHidden, ok, err)
	}
	if entry.Hidden == nil || !*entry.Hidden {
		t.Errorf("hidden = %v, want true", entry.Hidden)
	}
	if !strings.Contains(stdout.String(), "0") {
		t.Errorf("stdout = %q, want it to report 0 entries removed", stdout.String())
	}
}

func TestMixedEntriesOnlyOrphanedVisibleRemoved(t *testing.T) {
	newFakeHerdr(t)
	t.Setenv("STUB_HERDR_MISSING_PANE_IDS", paneOrphan+" "+paneHidden)
	stateDir := filepath.Join(t.TempDir(), "plugin-state")
	t.Setenv(stateDirEnvVar, stateDir)
	store := state.NewStore(stateDir)
	setEntry(t, store, keyLive, paneLive, false)
	setEntry(t, store, keyOrphan, paneOrphan, false)
	setEntry(t, store, keyHidden, paneHidden, true)

	var stdout, stderr bytes.Buffer
	code := Run(nil, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if _, ok, err := store.Get(keyOrphan); err != nil || ok {
		t.Errorf("entry %q still present: ok=%v err=%v", keyOrphan, ok, err)
	}
	if _, ok, err := store.Get(keyLive); err != nil || !ok {
		t.Errorf("entry %q missing or errored: ok=%v err=%v", keyLive, ok, err)
	}
	if _, ok, err := store.Get(keyHidden); err != nil || !ok {
		t.Errorf("entry %q missing or errored: ok=%v err=%v", keyHidden, ok, err)
	}
	if !strings.Contains(stdout.String(), "1") {
		t.Errorf("stdout = %q, want it to report 1 entry removed", stdout.String())
	}
}
