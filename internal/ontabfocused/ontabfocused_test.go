package ontabfocused

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maro114510/herdr-toggle-popup/internal/state"
)

// Test list:
//
// - HERDR_TAB_ID unset or empty: exits 0 without touching the registry or calling herdr
//   (os.Getenv makes unset and empty indistinguishable, so one test covers both)
// - a visible entry whose tab_id matches the focused tab is left untouched (no close call)
// - a visible entry whose tab_id differs from the focused tab is hidden and its Herdr pane closed
// - an already-hidden entry whose tab_id differs is left untouched (no redundant close call)
// - an entry with no known tab_id is left untouched regardless of the focused tab
// - multiple visible entries in different non-focused tabs are all hidden and closed
// - a close failure for a qualifying entry rolls the hidden flag back to visible, warns on
//   stderr, but still returns 0 and keeps processing the other qualifying entries
// - HERDR_PLUGIN_STATE_DIR unset while HERDR_TAB_ID is set: errors

const (
	stateDirEnvVar = "HERDR_PLUGIN_STATE_DIR"

	keyTabA = "workspace:ws1:shell"
	keyTabB = "workspace:ws1:git"
	keyTabC = "workspace:ws1:notes"

	paneTabA = "pane-a"
	paneTabB = "pane-b"
	paneTabC = "pane-c"

	tabA = "tab-a"
	tabB = "tab-b"
	tabC = "tab-c"
)

func setEntry(t *testing.T, store *state.Store, key, paneID, tabID string) {
	t.Helper()

	var tabIDPtr *string
	if tabID != "" {
		tabIDPtr = &tabID
	}
	entry := state.Entry{
		PaneID:          paneID,
		PluginID:        "maro114510.toggle-popup",
		Entrypoint:      "shell",
		Scope:           "workspace",
		WorkspaceID:     nil,
		TabID:           tabIDPtr,
		CreatedAtUnixMs: 1,
		Hidden:          nil,
	}
	if err := store.Set(key, entry); err != nil {
		t.Fatal(err)
	}
}

func hidden(entry state.Entry) bool {
	return entry.Hidden != nil && *entry.Hidden
}

func TestTabIDEmptyExitsZeroWithoutTouchingRegistry(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "plugin-state")
	t.Setenv(stateDirEnvVar, stateDir)
	t.Setenv(tabIDEnvVar, "")
	store := state.NewStore(stateDir)
	setEntry(t, store, keyTabA, paneTabA, tabB)

	var stdout, stderr bytes.Buffer
	code := Run(nil, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	entry, ok, err := store.Get(keyTabA)
	if err != nil || !ok {
		t.Fatalf("entry missing or errored: ok=%v err=%v", ok, err)
	}
	if hidden(entry) {
		t.Errorf("Hidden = %v, want untouched", entry.Hidden)
	}
}

func TestEntryInFocusedTabLeftUntouched(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "plugin-state")
	t.Setenv(stateDirEnvVar, stateDir)
	t.Setenv(tabIDEnvVar, tabA)
	logPath := newFakeHerdr(t)
	store := state.NewStore(stateDir)
	setEntry(t, store, keyTabA, paneTabA, tabA)

	var stdout, stderr bytes.Buffer
	code := Run(nil, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	entry, ok, err := store.Get(keyTabA)
	if err != nil || !ok {
		t.Fatalf("entry missing or errored: ok=%v err=%v", ok, err)
	}
	if hidden(entry) {
		t.Errorf("Hidden = %v, want untouched", entry.Hidden)
	}
	if log := readLog(t, logPath); log != "" {
		t.Errorf("herdr call log = %q, want empty (no close call)", log)
	}
}

func TestEntryInDifferentTabIsHiddenAndClosed(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "plugin-state")
	t.Setenv(stateDirEnvVar, stateDir)
	t.Setenv(tabIDEnvVar, tabA)
	logPath := newFakeHerdr(t)
	store := state.NewStore(stateDir)
	setEntry(t, store, keyTabB, paneTabB, tabB)

	var stdout, stderr bytes.Buffer
	code := Run(nil, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	entry, ok, err := store.Get(keyTabB)
	if err != nil || !ok {
		t.Fatalf("entry missing or errored: ok=%v err=%v", ok, err)
	}
	if !hidden(entry) {
		t.Errorf("Hidden = %v, want true", entry.Hidden)
	}
	want := "plugin pane close pane-b\n"
	if log := readLog(t, logPath); log != want {
		t.Errorf("herdr call log = %q, want %q", log, want)
	}
}

func TestAlreadyHiddenEntryInDifferentTabLeftUntouched(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "plugin-state")
	t.Setenv(stateDirEnvVar, stateDir)
	t.Setenv(tabIDEnvVar, tabA)
	logPath := newFakeHerdr(t)
	store := state.NewStore(stateDir)
	setEntry(t, store, keyTabB, paneTabB, tabB)
	if err := store.SetHidden(keyTabB, true); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run(nil, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if log := readLog(t, logPath); log != "" {
		t.Errorf("herdr call log = %q, want empty (already hidden, no redundant close)", log)
	}
}

func TestEntryWithUnknownTabIDLeftUntouched(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "plugin-state")
	t.Setenv(stateDirEnvVar, stateDir)
	t.Setenv(tabIDEnvVar, tabA)
	logPath := newFakeHerdr(t)
	store := state.NewStore(stateDir)
	setEntry(t, store, keyTabB, paneTabB, "")

	var stdout, stderr bytes.Buffer
	code := Run(nil, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	entry, ok, err := store.Get(keyTabB)
	if err != nil || !ok {
		t.Fatalf("entry missing or errored: ok=%v err=%v", ok, err)
	}
	if hidden(entry) {
		t.Errorf("Hidden = %v, want untouched (unknown tab_id)", entry.Hidden)
	}
	if log := readLog(t, logPath); log != "" {
		t.Errorf("herdr call log = %q, want empty", log)
	}
}

func TestMultipleEntriesInOtherTabsAreAllHiddenAndClosed(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "plugin-state")
	t.Setenv(stateDirEnvVar, stateDir)
	t.Setenv(tabIDEnvVar, tabA)
	logPath := newFakeHerdr(t)
	store := state.NewStore(stateDir)
	setEntry(t, store, keyTabA, paneTabA, tabA)
	setEntry(t, store, keyTabB, paneTabB, tabB)
	setEntry(t, store, keyTabC, paneTabC, tabC)

	var stdout, stderr bytes.Buffer
	code := Run(nil, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}

	entryA, _, _ := store.Get(keyTabA)
	if hidden(entryA) {
		t.Errorf("keyTabA Hidden = %v, want untouched (it's the focused tab)", entryA.Hidden)
	}
	entryB, _, _ := store.Get(keyTabB)
	if !hidden(entryB) {
		t.Errorf("keyTabB Hidden = %v, want true", entryB.Hidden)
	}
	entryC, _, _ := store.Get(keyTabC)
	if !hidden(entryC) {
		t.Errorf("keyTabC Hidden = %v, want true", entryC.Hidden)
	}

	log := readLog(t, logPath)
	if !strings.Contains(log, "plugin pane close pane-b") {
		t.Errorf("log = %q, want it to contain a close call for pane-b", log)
	}
	if !strings.Contains(log, "plugin pane close pane-c") {
		t.Errorf("log = %q, want it to contain a close call for pane-c", log)
	}
	if strings.Contains(log, "plugin pane close pane-a") {
		t.Errorf("log = %q, want no close call for pane-a (it's the focused tab)", log)
	}
}

func TestCloseFailureRollsBackHiddenAndKeepsProcessingOthers(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "plugin-state")
	t.Setenv(stateDirEnvVar, stateDir)
	t.Setenv(tabIDEnvVar, tabA)
	newFakeHerdr(t)
	t.Setenv("STUB_HERDR_CLOSE_FAIL_PANE_ID", paneTabB)
	store := state.NewStore(stateDir)
	setEntry(t, store, keyTabB, paneTabB, tabB)
	setEntry(t, store, keyTabC, paneTabC, tabC)

	var stdout, stderr bytes.Buffer
	code := Run(nil, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if stderr.Len() == 0 {
		t.Error("stderr is empty, want a warning about the failed close")
	}

	entryB, _, _ := store.Get(keyTabB)
	if hidden(entryB) {
		t.Errorf("keyTabB Hidden = %v, want false (rolled back after close failure)", entryB.Hidden)
	}
	entryC, _, _ := store.Get(keyTabC)
	if !hidden(entryC) {
		t.Errorf("keyTabC Hidden = %v, want true (unaffected by keyTabB's failure)", entryC.Hidden)
	}
}

func TestMissingStateDirWithTabIDSetErrors(t *testing.T) {
	t.Setenv(stateDirEnvVar, "")
	t.Setenv(tabIDEnvVar, tabA)

	var stdout, stderr bytes.Buffer
	code := Run(nil, &stdout, &stderr)

	if code == 0 {
		t.Errorf("exit code = 0, want non-zero")
	}
	if stderr.Len() == 0 {
		t.Errorf("stderr is empty, want an error message")
	}
}
