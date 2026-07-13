package state

import (
	_ "embed"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const (
	filePerm           = 0o600
	testPaneID1        = "pane-1"
	testPaneID2        = "pane-2"
	testScopeWorkspace = "workspace"
)

// bashFixtureJSON is byte-for-byte the popups.json produced by state.sh for
// two state_set calls (one workspace-scoped, one global-scoped) followed by
// a state_set_hidden on the global entry. It anchors Go/bash schema
// compatibility.
//
//go:embed testdata/bash_fixture.json
var bashFixtureJSON []byte

func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "plugin-state")
	return NewStore(dir), filepath.Join(dir, "popups.json")
}

func newEntry(paneID string) Entry {
	return Entry{
		PaneID:          paneID,
		PluginID:        "",
		Entrypoint:      "",
		Scope:           "",
		WorkspaceID:     nil,
		TabID:           nil,
		CreatedAtUnixMs: 0,
		Hidden:          nil,
	}
}

func TestRead_MissingFile(t *testing.T) {
	t.Parallel()

	store, file := newTestStore(t)

	reg, err := store.Read()
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if reg.Version != 1 || len(reg.Popups) != 0 {
		t.Errorf("Read() = %+v, want empty v1 registry", reg)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("popups.json should not be created by Read() on a missing file")
	}
}

func TestRead_ValidFile(t *testing.T) {
	t.Parallel()

	store, file := newTestStore(t)
	if err := os.MkdirAll(filepath.Dir(file), dirPerm); err != nil {
		t.Fatal(err)
	}
	content := `{"version":1,"popups":{"k":{"pane_id":"p1","plugin_id":"pl","entrypoint":"shell","scope":"global","workspace_id":null,"tab_id":null,"created_at_unix_ms":1}}}`
	if err := os.WriteFile(file, []byte(content), filePerm); err != nil {
		t.Fatal(err)
	}

	reg, err := store.Read()
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	entry, ok := reg.Popups["k"]
	if !ok || entry.PaneID != "p1" {
		t.Errorf("Read() = %+v, want entry k with pane_id p1", reg)
	}
}

func TestRead_CorruptFile_BacksUpAndReinitializes(t *testing.T) {
	t.Parallel()

	store, file := newTestStore(t)
	if err := os.MkdirAll(filepath.Dir(file), dirPerm); err != nil {
		t.Fatal(err)
	}
	corrupt := "not valid json{{{"
	if err := os.WriteFile(file, []byte(corrupt), filePerm); err != nil {
		t.Fatal(err)
	}

	reg, err := store.Read()
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if reg.Version != 1 || len(reg.Popups) != 0 {
		t.Errorf("Read() = %+v, want default empty registry", reg)
	}

	backup := assertSingleBackup(t, file)
	backupContent, err := os.ReadFile(filepath.Clean(backup))
	if err != nil {
		t.Fatal(err)
	}
	if string(backupContent) != corrupt {
		t.Errorf("backup content = %q, want %q", backupContent, corrupt)
	}

	reinit, err := os.ReadFile(filepath.Clean(file))
	if err != nil {
		t.Fatal(err)
	}
	if string(reinit) != `{"version":1,"popups":{}}` {
		t.Errorf("reinitialized content = %q, want default registry", reinit)
	}
}

func assertSingleBackup(t *testing.T, file string) string {
	t.Helper()

	matches, err := filepath.Glob(file + ".bak.*")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("backup files = %v, want exactly one", matches)
	}
	return matches[0]
}

func TestRead_MissingSchemaFields_TreatedAsCorrupt(t *testing.T) {
	t.Parallel()

	store, file := newTestStore(t)
	if err := os.MkdirAll(filepath.Dir(file), dirPerm); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(`{"foo":"bar"}`), filePerm); err != nil {
		t.Fatal(err)
	}

	reg, err := store.Read()
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if reg.Version != 1 || len(reg.Popups) != 0 {
		t.Errorf("Read() = %+v, want default empty registry", reg)
	}
}

func TestRead_BackupNameCollision_AppendsPidSuffix(t *testing.T) {
	t.Parallel()

	store, file := newTestStore(t)
	if err := os.MkdirAll(filepath.Dir(file), dirPerm); err != nil {
		t.Fatal(err)
	}

	fixedNow := time.Unix(1700000000, 0)
	store.now = func() time.Time { return fixedNow }
	store.pid = func() int { return 4242 }

	// Pre-create the timestamp-based backup name so Read() must fall back to
	// the pid-suffixed variant.
	collidingBackup := file + ".bak.1700000000"
	if err := os.WriteFile(collidingBackup, []byte("existing backup"), filePerm); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("not valid json{{{"), filePerm); err != nil {
		t.Fatal(err)
	}

	if _, err := store.Read(); err != nil {
		t.Fatalf("Read() error = %v", err)
	}

	wantBackup := collidingBackup + ".4242"
	if _, err := os.Stat(wantBackup); err != nil {
		t.Errorf("expected pid-suffixed backup at %s, stat error = %v", wantBackup, err)
	}
	got, err := os.ReadFile(filepath.Clean(collidingBackup))
	if err != nil || string(got) != "existing backup" {
		t.Errorf("pre-existing backup was overwritten: content=%q err=%v", got, err)
	}
}

func TestRead_BackupRenameFailure_LeavesFileIntact(t *testing.T) {
	t.Parallel()

	store, file := newTestStore(t)
	if err := os.MkdirAll(filepath.Dir(file), dirPerm); err != nil {
		t.Fatal(err)
	}
	corrupt := "not valid json{{{"
	if err := os.WriteFile(file, []byte(corrupt), filePerm); err != nil {
		t.Fatal(err)
	}

	store.rename = func(_, _ string) error { return errors.New("simulated rename failure") }

	if _, err := store.Read(); err == nil {
		t.Fatal("Read() error = nil, want error on backup rename failure")
	}

	got, err := os.ReadFile(filepath.Clean(file))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != corrupt {
		t.Errorf("original file was modified: content = %q", got)
	}
}

func TestWriteRegistry_CreatesParentDirWhenMissing(t *testing.T) {
	t.Parallel()

	store, file := newTestStore(t)
	if _, err := os.Stat(filepath.Dir(file)); !os.IsNotExist(err) {
		t.Fatalf("state dir should not exist yet")
	}

	if err := store.WriteRegistry(defaultRegistry()); err != nil {
		t.Fatalf("WriteRegistry() error = %v", err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Errorf("popups.json was not created: %v", err)
	}
}

func TestWriteRegistry_NoLeftoverTempFiles(t *testing.T) {
	t.Parallel()

	store, file := newTestStore(t)

	if err := store.WriteRegistry(defaultRegistry()); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(filepath.Dir(file))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp.") {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}

func TestWithLock_SerializesAcrossProcesses(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "plugin-state")
	firstMarker := filepath.Join(dir, "first.locked")
	secondMarker := filepath.Join(dir, "second.locked")
	firstRelease := filepath.Join(dir, "first.release")
	secondRelease := filepath.Join(dir, "second.release")

	first := startLockHelper(t, dir, firstMarker, firstRelease)
	waitForFile(t, firstMarker)

	second := startLockHelper(t, dir, secondMarker, secondRelease)
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(secondMarker); err == nil {
		t.Fatal("second process entered the locked section before the first released it")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat second marker: %v", err)
	}

	if err := os.WriteFile(firstRelease, []byte("release"), filePerm); err != nil {
		t.Fatal(err)
	}
	if err := first.Wait(); err != nil {
		t.Fatalf("first helper: %v", err)
	}

	waitForFile(t, secondMarker)
	if err := os.WriteFile(secondRelease, []byte("release"), filePerm); err != nil {
		t.Fatal(err)
	}
	if err := second.Wait(); err != nil {
		t.Fatalf("second helper: %v", err)
	}
}

func startLockHelper(t *testing.T, dir, marker, release string) *exec.Cmd {
	t.Helper()

	//nolint:gosec // test helper intentionally re-execs this trusted test binary.
	cmd := exec.Command(os.Args[0], "-test.run=TestWithLock_HelperProcess", "--", dir, marker, release)
	cmd.Env = append(os.Environ(), "GO_WANT_LOCK_HELPER_PROCESS=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return cmd
}

func waitForFile(t *testing.T, path string) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat %s: %v", path, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

//nolint:paralleltest // helper may os.Exit and is only run in a subprocess.
func TestWithLock_HelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_LOCK_HELPER_PROCESS") != "1" {
		return
	}
	if len(os.Args) < 4 {
		t.Fatalf("helper args = %v", os.Args)
	}
	args := os.Args[len(os.Args)-3:]

	store := NewStore(args[0])
	if err := store.WithLock(func() error {
		//nolint:gosec // helper paths are created by the parent test under t.TempDir.
		if err := os.WriteFile(args[1], []byte("locked"), filePerm); err != nil {
			return err
		}
		for {
			//nolint:gosec // helper paths are created by the parent test under t.TempDir.
			if _, err := os.Stat(args[2]); err == nil {
				return nil
			} else if !os.IsNotExist(err) {
				return err
			}
			time.Sleep(10 * time.Millisecond)
		}
	}); err != nil {
		t.Fatal(err)
	}
	os.Exit(0)
}

func seedStore(t *testing.T) (*Store, string) {
	t.Helper()

	store, file := newTestStore(t)
	if err := os.MkdirAll(filepath.Dir(file), dirPerm); err != nil {
		t.Fatal(err)
	}
	content := `{"version":1,"popups":{"workspace:ws1:shell":{"pane_id":"pane-1","plugin_id":"pl","entrypoint":"shell","scope":"workspace","workspace_id":"ws1","tab_id":null,"created_at_unix_ms":1}}}`
	if err := os.WriteFile(file, []byte(content), filePerm); err != nil {
		t.Fatal(err)
	}
	return store, file
}

func TestGet_ReturnsEntryForExistingKey(t *testing.T) {
	t.Parallel()

	store, _ := seedStore(t)

	entry, ok, err := store.Get("workspace:ws1:shell")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !ok || entry.PaneID != testPaneID1 {
		t.Errorf("Get() = %+v, ok=%v, want %s", entry, ok, testPaneID1)
	}
}

func TestGet_MissingKey(t *testing.T) {
	t.Parallel()

	store, _ := seedStore(t)

	_, ok, err := store.Get("workspace:missing:shell")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if ok {
		t.Error("Get() ok = true, want false for a key that was never set")
	}
}

func TestSet_RoundTrip(t *testing.T) {
	t.Parallel()

	store, _ := newTestStore(t)

	entry := Entry{
		PaneID:          testPaneID1,
		PluginID:        "maro114510.toggle-popup",
		Entrypoint:      "shell",
		Scope:           testScopeWorkspace,
		WorkspaceID:     new("ws1"),
		TabID:           nil,
		CreatedAtUnixMs: 1720000000000,
		Hidden:          nil,
	}
	if err := store.Set("workspace:ws1:shell", entry); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	got, ok, err := store.Get("workspace:ws1:shell")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !ok {
		t.Fatal("Get() ok = false, want true")
	}
	if !reflect.DeepEqual(got, entry) {
		t.Errorf("Get() = %+v, want %+v", got, entry)
	}
}

func TestSet_PreservesOtherEntries(t *testing.T) {
	t.Parallel()

	store, _ := newTestStore(t)

	if err := store.Set("workspace:ws1:shell", newEntry(testPaneID1)); err != nil {
		t.Fatal(err)
	}
	if err := store.Set("workspace:ws2:shell", newEntry(testPaneID2)); err != nil {
		t.Fatal(err)
	}

	e1, ok, _ := store.Get("workspace:ws1:shell")
	if !ok || e1.PaneID != testPaneID1 {
		t.Errorf("Get(ws1) = %+v, ok=%v, want %s", e1, ok, testPaneID1)
	}
	e2, ok, _ := store.Get("workspace:ws2:shell")
	if !ok || e2.PaneID != testPaneID2 {
		t.Errorf("Get(ws2) = %+v, ok=%v, want %s", e2, ok, testPaneID2)
	}
}

func TestSet_OverwritesHidden(t *testing.T) {
	t.Parallel()

	store, _ := newTestStore(t)

	if err := store.Set("k", newEntry(testPaneID1)); err != nil {
		t.Fatal(err)
	}
	if err := store.SetHidden("k", true); err != nil {
		t.Fatal(err)
	}
	if err := store.Set("k", newEntry(testPaneID1)); err != nil {
		t.Fatal(err)
	}

	got, _, _ := store.Get("k")
	if got.Hidden != nil {
		t.Errorf("Hidden = %v, want nil after Set overwrites the entry", got.Hidden)
	}
}

func TestSet_WritesDocumentedFieldSet(t *testing.T) {
	t.Parallel()

	store, file := newTestStore(t)

	entry := Entry{
		PaneID:          testPaneID1,
		PluginID:        "maro114510.toggle-popup",
		Entrypoint:      "shell",
		Scope:           testScopeWorkspace,
		WorkspaceID:     new("ws1"),
		TabID:           nil,
		CreatedAtUnixMs: 1,
		Hidden:          nil,
	}
	if err := store.Set("workspace:ws1:shell", entry); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Clean(file))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	popups, ok := doc["popups"].(map[string]any)
	if !ok {
		t.Fatalf("popups = %v, want an object", doc["popups"])
	}
	fields, ok := popups["workspace:ws1:shell"].(map[string]any)
	if !ok {
		t.Fatalf("entry = %v, want an object", popups["workspace:ws1:shell"])
	}
	wantKeys := []string{"pane_id", "plugin_id", "entrypoint", "scope", "workspace_id", "tab_id", "created_at_unix_ms"}
	if len(fields) != len(wantKeys) {
		t.Fatalf("entry fields = %v, want exactly %v", fields, wantKeys)
	}
	for _, k := range wantKeys {
		if _, ok := fields[k]; !ok {
			t.Errorf("entry missing field %q", k)
		}
	}
}

func TestSet_CreatesStateDirWhenMissing(t *testing.T) {
	t.Parallel()

	store, file := newTestStore(t)
	if _, err := os.Stat(filepath.Dir(file)); !os.IsNotExist(err) {
		t.Fatalf("state dir should not exist yet")
	}

	if err := store.Set("k", newEntry(testPaneID1)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Errorf("popups.json was not created: %v", err)
	}
}

func TestDelete_RemovesEntry(t *testing.T) {
	t.Parallel()

	store, _ := newTestStore(t)
	if err := store.Set("k", newEntry(testPaneID1)); err != nil {
		t.Fatal(err)
	}

	if err := store.Delete("k"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	_, ok, _ := store.Get("k")
	if ok {
		t.Error("Get() ok = true after Delete, want false")
	}
}

func TestDelete_NonexistentKey_NoError(t *testing.T) {
	t.Parallel()

	store, _ := newTestStore(t)

	if err := store.Delete("missing"); err != nil {
		t.Errorf("Delete() error = %v, want nil for idempotent delete", err)
	}
}

func TestDeleteByPaneID_RemovesMatching(t *testing.T) {
	t.Parallel()

	store, _ := newTestStore(t)
	if err := store.Set("ws1", newEntry(testPaneID1)); err != nil {
		t.Fatal(err)
	}

	if err := store.DeleteByPaneID(testPaneID1); err != nil {
		t.Fatalf("DeleteByPaneID() error = %v", err)
	}

	_, ok, _ := store.Get("ws1")
	if ok {
		t.Error("Get() ok = true after DeleteByPaneID, want false")
	}
}

func TestDeleteByPaneID_LeavesOthersUntouched(t *testing.T) {
	t.Parallel()

	store, _ := newTestStore(t)
	if err := store.Set("ws1", newEntry(testPaneID1)); err != nil {
		t.Fatal(err)
	}
	if err := store.Set("ws2", newEntry(testPaneID2)); err != nil {
		t.Fatal(err)
	}

	if err := store.DeleteByPaneID(testPaneID1); err != nil {
		t.Fatal(err)
	}

	got, ok, _ := store.Get("ws2")
	if !ok || got.PaneID != testPaneID2 {
		t.Errorf("Get(ws2) = %+v, ok=%v, want %s untouched", got, ok, testPaneID2)
	}
}

func TestDeleteByPaneID_NoMatch_NoError(t *testing.T) {
	t.Parallel()

	store, _ := newTestStore(t)
	if err := store.Set("ws1", newEntry(testPaneID1)); err != nil {
		t.Fatal(err)
	}

	if err := store.DeleteByPaneID("pane-missing"); err != nil {
		t.Errorf("DeleteByPaneID() error = %v, want nil", err)
	}
	_, ok, _ := store.Get("ws1")
	if !ok {
		t.Error("unrelated entry was removed")
	}
}

func TestDeleteByPaneID_EmptyRegistry_NoError(t *testing.T) {
	t.Parallel()

	store, _ := newTestStore(t)

	if err := store.DeleteByPaneID(testPaneID1); err != nil {
		t.Errorf("DeleteByPaneID() error = %v, want nil on empty registry", err)
	}
}

func TestSetHidden_SetsFlagWithoutTouchingOtherFields(t *testing.T) {
	t.Parallel()

	store, _ := newTestStore(t)
	entry := Entry{
		PaneID:          testPaneID1,
		PluginID:        "",
		Entrypoint:      "",
		Scope:           testScopeWorkspace,
		WorkspaceID:     nil,
		TabID:           nil,
		CreatedAtUnixMs: 0,
		Hidden:          nil,
	}
	if err := store.Set("k", entry); err != nil {
		t.Fatal(err)
	}

	if err := store.SetHidden("k", true); err != nil {
		t.Fatalf("SetHidden() error = %v", err)
	}

	got, ok, _ := store.Get("k")
	if !ok || got.Hidden == nil || !*got.Hidden || got.PaneID != testPaneID1 || got.Scope != testScopeWorkspace {
		t.Errorf("Get() = %+v, ok=%v, want hidden=true with other fields intact", got, ok)
	}
}

func TestSetHidden_CanFlipBackToFalse(t *testing.T) {
	t.Parallel()

	store, _ := newTestStore(t)
	if err := store.Set("k", newEntry(testPaneID1)); err != nil {
		t.Fatal(err)
	}
	if err := store.SetHidden("k", true); err != nil {
		t.Fatal(err)
	}
	if err := store.SetHidden("k", false); err != nil {
		t.Fatal(err)
	}

	got, _, _ := store.Get("k")
	if got.Hidden == nil || *got.Hidden {
		t.Errorf("Hidden = %v, want false", got.Hidden)
	}
}

func TestSetHidden_NonexistentKey_NoErrorNoCreate(t *testing.T) {
	t.Parallel()

	store, _ := newTestStore(t)

	if err := store.SetHidden("missing", true); err != nil {
		t.Fatalf("SetHidden() error = %v, want nil no-op", err)
	}
	_, ok, _ := store.Get("missing")
	if ok {
		t.Error("SetHidden() created an entry for a missing key")
	}
}

func TestSetHidden_WritesBooleanNotString(t *testing.T) {
	t.Parallel()

	store, file := newTestStore(t)
	if err := store.Set("k", newEntry(testPaneID1)); err != nil {
		t.Fatal(err)
	}
	if err := store.SetHidden("k", true); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Clean(file))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"hidden":true`) {
		t.Errorf("raw registry = %s, want a boolean hidden field", raw)
	}
}

func TestStateDirFromEnv(t *testing.T) {
	cases := map[string]struct {
		value   string
		wantErr bool
		want    string
	}{
		"unset returns an error": {
			value:   "",
			wantErr: true,
			want:    "",
		},
		"set returns the directory": {
			value:   "/tmp/some-dir",
			wantErr: false,
			want:    "/tmp/some-dir",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("HERDR_PLUGIN_STATE_DIR", tc.value)

			dir, err := StateDirFromEnv()
			if tc.wantErr {
				if err == nil {
					t.Error("StateDirFromEnv() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("StateDirFromEnv() error = %v", err)
			}
			if dir != tc.want {
				t.Errorf("StateDirFromEnv() = %q, want %q", dir, tc.want)
			}
		})
	}
}

func seedBashFixture(t *testing.T) *Store {
	t.Helper()

	store, file := newTestStore(t)
	if err := os.MkdirAll(filepath.Dir(file), dirPerm); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, bashFixtureJSON, filePerm); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestBashFixture_ReadsWorkspaceEntry(t *testing.T) {
	t.Parallel()

	store := seedBashFixture(t)

	ws1, ok, err := store.Get("workspace:ws1:shell")
	if err != nil || !ok {
		t.Fatalf("Get(workspace:ws1:shell) ok=%v err=%v", ok, err)
	}
	if ws1.PaneID != testPaneID1 || ws1.WorkspaceID == nil || *ws1.WorkspaceID != "ws1" || ws1.TabID != nil {
		t.Errorf("ws1 entry = %+v, mismatched with bash fixture", ws1)
	}
}

func TestBashFixture_ReadsHiddenGlobalEntry(t *testing.T) {
	t.Parallel()

	store := seedBashFixture(t)

	global, ok, err := store.Get("global:shell")
	if err != nil || !ok {
		t.Fatalf("Get(global:shell) ok=%v err=%v", ok, err)
	}
	if global.Hidden == nil || !*global.Hidden {
		t.Errorf("global entry hidden = %v, want true", global.Hidden)
	}
}

func TestBashFixture_DeleteLeavesOtherEntryUntouched(t *testing.T) {
	t.Parallel()

	store := seedBashFixture(t)

	if err := store.Delete("workspace:ws1:shell"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := store.Get("workspace:ws1:shell"); ok {
		t.Error("workspace:ws1:shell still present after Delete")
	}
	if _, ok, _ := store.Get("global:shell"); !ok {
		t.Error("global:shell should be untouched by deleting workspace:ws1:shell")
	}
}

// TestBashFixture_InteropWithStateSh drives a real round trip through
// state.sh (relative to this package directory, which is go test's working
// directory): bash writes, Go reads/updates, bash reads the result back. The
// two bash invocations are fully literal (no interpolated command text) so
// there's nothing but a fixed, repo-local script for the subprocess to run.
// It skips gracefully when bash/jq or the script aren't available (e.g. a
// minimal CI image), since the testdata/bash_fixture.json-based tests above
// already cover schema compatibility deterministically.
func skipUnlessBashInteropAvailable(t *testing.T) {
	t.Helper()

	if _, err := os.Stat("../../state.sh"); err != nil {
		t.Skip("state.sh not found, skipping bash interop test")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found, skipping bash interop test")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not found, skipping bash interop test")
	}
}

func bashStateSet(t *testing.T, env []string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "bash", "-c",
		`source "../../state.sh" && state_set "workspace:ws1:shell" "pane-1" "maro114510.toggle-popup" "shell" "workspace" "ws1" "" 1720000000000`)
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bash state_set failed: %v\noutput: %s", err, out)
	}
}

func bashStateGet(t *testing.T, env []string) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "bash", "-c",
		`source "../../state.sh" && state_get "workspace:ws1:shell"`)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash state_get failed: %v\noutput: %s", err, out)
	}
	return string(out)
}

func TestBashFixture_InteropWithStateSh(t *testing.T) {
	t.Parallel()
	skipUnlessBashInteropAvailable(t)

	stateDir := filepath.Join(t.TempDir(), "plugin-state")
	env := append(os.Environ(), "HERDR_PLUGIN_STATE_DIR="+stateDir)
	bashStateSet(t, env)

	store := NewStore(stateDir)
	entry, ok, err := store.Get("workspace:ws1:shell")
	if err != nil || !ok {
		t.Fatalf("Go Get() after bash write: ok=%v err=%v", ok, err)
	}
	if entry.PaneID != testPaneID1 {
		t.Fatalf("Go Get() = %+v, want %s", entry, testPaneID1)
	}

	entry.PaneID = "pane-updated"
	if err := store.Set("workspace:ws1:shell", entry); err != nil {
		t.Fatalf("Go Set() error = %v", err)
	}

	out := bashStateGet(t, env)
	if !strings.Contains(out, "pane-updated") {
		t.Errorf("bash state_get after Go update = %q, want it to contain pane-updated", out)
	}
}
