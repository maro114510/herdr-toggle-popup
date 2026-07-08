package state

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "plugin-state")
	return NewStore(dir), filepath.Join(dir, "popups.json")
}

func ptr(s string) *string { return &s }

func TestRead_MissingFile(t *testing.T) {
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
	store, file := newTestStore(t)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	content := `{"version":1,"popups":{"k":{"pane_id":"p1","plugin_id":"pl","entrypoint":"shell","scope":"global","workspace_id":null,"tab_id":null,"created_at_unix_ms":1}}}`
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
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
	store, file := newTestStore(t)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	corrupt := "not valid json{{{"
	if err := os.WriteFile(file, []byte(corrupt), 0o644); err != nil {
		t.Fatal(err)
	}

	reg, err := store.Read()
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if reg.Version != 1 || len(reg.Popups) != 0 {
		t.Errorf("Read() = %+v, want default empty registry", reg)
	}

	matches, _ := filepath.Glob(file + ".bak.*")
	if len(matches) != 1 {
		t.Fatalf("backup files = %v, want exactly one", matches)
	}
	backupContent, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(backupContent) != corrupt {
		t.Errorf("backup content = %q, want %q", backupContent, corrupt)
	}

	reinit, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(reinit) != `{"version":1,"popups":{}}` {
		t.Errorf("reinitialized content = %q, want default registry", reinit)
	}
}

func TestRead_MissingSchemaFields_TreatedAsCorrupt(t *testing.T) {
	store, file := newTestStore(t)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(`{"foo":"bar"}`), 0o644); err != nil {
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
	store, file := newTestStore(t)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}

	fixedNow := time.Unix(1700000000, 0)
	origNow, origPid := now, pid
	now = func() time.Time { return fixedNow }
	pid = func() int { return 4242 }
	t.Cleanup(func() { now, pid = origNow, origPid })

	// Pre-create the timestamp-based backup name so Read() must fall back to
	// the pid-suffixed variant.
	collidingBackup := file + ".bak.1700000000"
	if err := os.WriteFile(collidingBackup, []byte("existing backup"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("not valid json{{{"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := store.Read(); err != nil {
		t.Fatalf("Read() error = %v", err)
	}

	wantBackup := collidingBackup + ".4242"
	if _, err := os.Stat(wantBackup); err != nil {
		t.Errorf("expected pid-suffixed backup at %s, stat error = %v", wantBackup, err)
	}
	if got, err := os.ReadFile(collidingBackup); err != nil || string(got) != "existing backup" {
		t.Errorf("pre-existing backup was overwritten: content=%q err=%v", got, err)
	}
}

func TestRead_BackupRenameFailure_LeavesFileIntact(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permission bits don't govern rename on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root bypasses directory permission checks")
	}

	store, file := newTestStore(t)
	dir := filepath.Dir(file)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	corrupt := "not valid json{{{"
	if err := os.WriteFile(file, []byte(corrupt), 0o644); err != nil {
		t.Fatal(err)
	}

	// Remove write permission on the state dir so os.Rename cannot create the
	// backup directory entry, regardless of the backup name chosen.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })

	if _, err := store.Read(); err == nil {
		t.Fatal("Read() error = nil, want error on backup rename failure")
	}

	got, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != corrupt {
		t.Errorf("original file was modified: content = %q", got)
	}
}

func TestWriteRegistry(t *testing.T) {
	cases := map[string]func(t *testing.T){
		"creates the parent directory when missing": func(t *testing.T) {
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
		},
		"leaves no leftover temp files": func(t *testing.T) {
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
		},
	}

	for name, tc := range cases {
		t.Run(name, tc)
	}
}

func TestGet(t *testing.T) {
	seed := func(t *testing.T) (*Store, string) {
		store, file := newTestStore(t)
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		content := `{"version":1,"popups":{"workspace:ws1:shell":{"pane_id":"pane-1","plugin_id":"pl","entrypoint":"shell","scope":"workspace","workspace_id":"ws1","tab_id":null,"created_at_unix_ms":1}}}`
		if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return store, file
	}

	cases := map[string]func(t *testing.T){
		"returns the entry for an existing key": func(t *testing.T) {
			store, _ := seed(t)

			entry, ok, err := store.Get("workspace:ws1:shell")
			if err != nil {
				t.Fatalf("Get() error = %v", err)
			}
			if !ok || entry.PaneID != "pane-1" {
				t.Errorf("Get() = %+v, ok=%v, want pane-1", entry, ok)
			}
		},
		"reports not found for a missing key": func(t *testing.T) {
			store, _ := seed(t)

			_, ok, err := store.Get("workspace:missing:shell")
			if err != nil {
				t.Fatalf("Get() error = %v", err)
			}
			if ok {
				t.Error("Get() ok = true, want false for a key that was never set")
			}
		},
	}

	for name, tc := range cases {
		t.Run(name, tc)
	}
}

func TestSet(t *testing.T) {
	cases := map[string]func(t *testing.T){
		"stores a new entry retrievable via Get": func(t *testing.T) {
			store, _ := newTestStore(t)

			entry := Entry{
				PaneID:          "pane-1",
				PluginID:        "maro114510.toggle-popup",
				Entrypoint:      "shell",
				Scope:           "workspace",
				WorkspaceID:     ptr("ws1"),
				TabID:           nil,
				CreatedAtUnixMs: 1720000000000,
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
			if got.PaneID != "pane-1" || got.PluginID != "maro114510.toggle-popup" ||
				got.Entrypoint != "shell" || got.Scope != "workspace" ||
				got.WorkspaceID == nil || *got.WorkspaceID != "ws1" ||
				got.TabID != nil || got.CreatedAtUnixMs != 1720000000000 {
				t.Errorf("Get() = %+v, want round-tripped entry", got)
			}
		},
		"preserves other existing entries": func(t *testing.T) {
			store, _ := newTestStore(t)

			if err := store.Set("workspace:ws1:shell", Entry{PaneID: "pane-1"}); err != nil {
				t.Fatal(err)
			}
			if err := store.Set("workspace:ws2:shell", Entry{PaneID: "pane-2"}); err != nil {
				t.Fatal(err)
			}

			e1, ok, _ := store.Get("workspace:ws1:shell")
			if !ok || e1.PaneID != "pane-1" {
				t.Errorf("Get(ws1) = %+v, ok=%v, want pane-1", e1, ok)
			}
			e2, ok, _ := store.Get("workspace:ws2:shell")
			if !ok || e2.PaneID != "pane-2" {
				t.Errorf("Get(ws2) = %+v, ok=%v, want pane-2", e2, ok)
			}
		},
		"overwrites a previous hidden flag": func(t *testing.T) {
			store, _ := newTestStore(t)

			if err := store.Set("k", Entry{PaneID: "pane-1"}); err != nil {
				t.Fatal(err)
			}
			if err := store.SetHidden("k", true); err != nil {
				t.Fatal(err)
			}
			if err := store.Set("k", Entry{PaneID: "pane-1"}); err != nil {
				t.Fatal(err)
			}

			got, _, _ := store.Get("k")
			if got.Hidden != nil {
				t.Errorf("Hidden = %v, want nil after Set overwrites the entry", got.Hidden)
			}
		},
		"writes exactly the documented field set": func(t *testing.T) {
			store, file := newTestStore(t)

			entry := Entry{
				PaneID:          "pane-1",
				PluginID:        "maro114510.toggle-popup",
				Entrypoint:      "shell",
				Scope:           "workspace",
				WorkspaceID:     ptr("ws1"),
				CreatedAtUnixMs: 1,
			}
			if err := store.Set("workspace:ws1:shell", entry); err != nil {
				t.Fatal(err)
			}

			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var doc map[string]any
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			popups := doc["popups"].(map[string]any)
			fields := popups["workspace:ws1:shell"].(map[string]any)
			wantKeys := []string{"pane_id", "plugin_id", "entrypoint", "scope", "workspace_id", "tab_id", "created_at_unix_ms"}
			if len(fields) != len(wantKeys) {
				t.Fatalf("entry fields = %v, want exactly %v", fields, wantKeys)
			}
			for _, k := range wantKeys {
				if _, ok := fields[k]; !ok {
					t.Errorf("entry missing field %q", k)
				}
			}
		},
		"creates the state directory when missing": func(t *testing.T) {
			store, file := newTestStore(t)
			if _, err := os.Stat(filepath.Dir(file)); !os.IsNotExist(err) {
				t.Fatalf("state dir should not exist yet")
			}

			if err := store.Set("k", Entry{PaneID: "pane-1"}); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(file); err != nil {
				t.Errorf("popups.json was not created: %v", err)
			}
		},
	}

	for name, tc := range cases {
		t.Run(name, tc)
	}
}

func TestDelete(t *testing.T) {
	cases := map[string]func(t *testing.T){
		"removes an existing entry": func(t *testing.T) {
			store, _ := newTestStore(t)
			if err := store.Set("k", Entry{PaneID: "pane-1"}); err != nil {
				t.Fatal(err)
			}

			if err := store.Delete("k"); err != nil {
				t.Fatalf("Delete() error = %v", err)
			}

			_, ok, _ := store.Get("k")
			if ok {
				t.Error("Get() ok = true after Delete, want false")
			}
		},
		"is idempotent for a nonexistent key": func(t *testing.T) {
			store, _ := newTestStore(t)

			if err := store.Delete("missing"); err != nil {
				t.Errorf("Delete() error = %v, want nil for idempotent delete", err)
			}
		},
	}

	for name, tc := range cases {
		t.Run(name, tc)
	}
}

func TestDeleteByPaneID(t *testing.T) {
	cases := map[string]func(t *testing.T){
		"removes entries with a matching pane id": func(t *testing.T) {
			store, _ := newTestStore(t)
			if err := store.Set("ws1", Entry{PaneID: "pane-1"}); err != nil {
				t.Fatal(err)
			}

			if err := store.DeleteByPaneID("pane-1"); err != nil {
				t.Fatalf("DeleteByPaneID() error = %v", err)
			}

			_, ok, _ := store.Get("ws1")
			if ok {
				t.Error("Get() ok = true after DeleteByPaneID, want false")
			}
		},
		"leaves other entries untouched": func(t *testing.T) {
			store, _ := newTestStore(t)
			if err := store.Set("ws1", Entry{PaneID: "pane-1"}); err != nil {
				t.Fatal(err)
			}
			if err := store.Set("ws2", Entry{PaneID: "pane-2"}); err != nil {
				t.Fatal(err)
			}

			if err := store.DeleteByPaneID("pane-1"); err != nil {
				t.Fatal(err)
			}

			got, ok, _ := store.Get("ws2")
			if !ok || got.PaneID != "pane-2" {
				t.Errorf("Get(ws2) = %+v, ok=%v, want pane-2 untouched", got, ok)
			}
		},
		"is a no-op when nothing matches": func(t *testing.T) {
			store, _ := newTestStore(t)
			if err := store.Set("ws1", Entry{PaneID: "pane-1"}); err != nil {
				t.Fatal(err)
			}

			if err := store.DeleteByPaneID("pane-missing"); err != nil {
				t.Errorf("DeleteByPaneID() error = %v, want nil", err)
			}
			_, ok, _ := store.Get("ws1")
			if !ok {
				t.Error("unrelated entry was removed")
			}
		},
		"is a no-op on an empty registry": func(t *testing.T) {
			store, _ := newTestStore(t)

			if err := store.DeleteByPaneID("pane-1"); err != nil {
				t.Errorf("DeleteByPaneID() error = %v, want nil on empty registry", err)
			}
		},
	}

	for name, tc := range cases {
		t.Run(name, tc)
	}
}

func TestSetHidden(t *testing.T) {
	cases := map[string]func(t *testing.T){
		"sets the hidden flag without touching other fields": func(t *testing.T) {
			store, _ := newTestStore(t)
			if err := store.Set("k", Entry{PaneID: "pane-1", Scope: "workspace"}); err != nil {
				t.Fatal(err)
			}

			if err := store.SetHidden("k", true); err != nil {
				t.Fatalf("SetHidden() error = %v", err)
			}

			got, ok, _ := store.Get("k")
			if !ok || got.Hidden == nil || !*got.Hidden || got.PaneID != "pane-1" || got.Scope != "workspace" {
				t.Errorf("Get() = %+v, ok=%v, want hidden=true with other fields intact", got, ok)
			}
		},
		"can flip an entry back to not hidden": func(t *testing.T) {
			store, _ := newTestStore(t)
			if err := store.Set("k", Entry{PaneID: "pane-1"}); err != nil {
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
		},
		"is a no-op for a nonexistent key": func(t *testing.T) {
			store, _ := newTestStore(t)

			if err := store.SetHidden("missing", true); err != nil {
				t.Fatalf("SetHidden() error = %v, want nil no-op", err)
			}
			_, ok, _ := store.Get("missing")
			if ok {
				t.Error("SetHidden() created an entry for a missing key")
			}
		},
		"writes a boolean, not a string": func(t *testing.T) {
			store, file := newTestStore(t)
			if err := store.Set("k", Entry{PaneID: "pane-1"}); err != nil {
				t.Fatal(err)
			}
			if err := store.SetHidden("k", true); err != nil {
				t.Fatal(err)
			}

			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(raw), `"hidden":true`) {
				t.Errorf("raw registry = %s, want a boolean hidden field", raw)
			}
		},
	}

	for name, tc := range cases {
		t.Run(name, tc)
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
		},
		"set returns the directory": {
			value: "/tmp/some-dir",
			want:  "/tmp/some-dir",
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

// bashFixture is byte-for-byte the popups.json produced by state.sh for two
// state_set calls (one workspace-scoped, one global-scoped) followed by a
// state_set_hidden on the global entry. It anchors Go/bash schema compatibility.
const bashFixture = `{"version":1,"popups":{"workspace:ws1:shell":{"pane_id":"pane-1","plugin_id":"maro114510.toggle-popup","entrypoint":"shell","scope":"workspace","workspace_id":"ws1","tab_id":null,"created_at_unix_ms":1720000000000},"global:shell":{"pane_id":"pane-2","plugin_id":"maro114510.toggle-popup","entrypoint":"shell","scope":"global","workspace_id":null,"tab_id":null,"created_at_unix_ms":1720000001000,"hidden":true}}}`

func TestBashFixture_ReadAndUpdate(t *testing.T) {
	store, file := newTestStore(t)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(bashFixture), 0o644); err != nil {
		t.Fatal(err)
	}

	ws1, ok, err := store.Get("workspace:ws1:shell")
	if err != nil || !ok {
		t.Fatalf("Get(workspace:ws1:shell) ok=%v err=%v", ok, err)
	}
	if ws1.PaneID != "pane-1" || ws1.WorkspaceID == nil || *ws1.WorkspaceID != "ws1" || ws1.TabID != nil {
		t.Errorf("ws1 entry = %+v, mismatched with bash fixture", ws1)
	}

	global, ok, err := store.Get("global:shell")
	if err != nil || !ok {
		t.Fatalf("Get(global:shell) ok=%v err=%v", ok, err)
	}
	if global.Hidden == nil || !*global.Hidden {
		t.Errorf("global entry hidden = %v, want true", global.Hidden)
	}

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
// state.sh: bash writes, Go reads/updates, bash reads the result back. It
// skips gracefully when bash/jq or the script aren't available (e.g. a
// minimal CI image), since the static bashFixture test above already covers
// schema compatibility deterministically.
func TestBashFixture_InteropWithStateSh(t *testing.T) {
	stateSh, err := filepath.Abs("../../state.sh")
	if err != nil || !fileExists(stateSh) {
		t.Skip("state.sh not found, skipping bash interop test")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found, skipping bash interop test")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not found, skipping bash interop test")
	}

	stateDir := filepath.Join(t.TempDir(), "plugin-state")
	runBash(t, stateSh, stateDir, `state_set "workspace:ws1:shell" "pane-1" "maro114510.toggle-popup" "shell" "workspace" "ws1" "" 1720000000000`)

	store := NewStore(stateDir)
	entry, ok, err := store.Get("workspace:ws1:shell")
	if err != nil || !ok {
		t.Fatalf("Go Get() after bash write: ok=%v err=%v", ok, err)
	}
	if entry.PaneID != "pane-1" {
		t.Fatalf("Go Get() = %+v, want pane-1", entry)
	}

	entry.PaneID = "pane-updated"
	if err := store.Set("workspace:ws1:shell", entry); err != nil {
		t.Fatalf("Go Set() error = %v", err)
	}

	out := runBash(t, stateSh, stateDir, `state_get "workspace:ws1:shell"`)
	if !strings.Contains(out, "pane-updated") {
		t.Errorf("bash state_get after Go update = %q, want it to contain pane-updated", out)
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func runBash(t *testing.T, stateSh, stateDir, script string) string {
	t.Helper()
	cmd := exec.Command("bash", "-c", `source "$1" && `+script, "--", stateSh)
	cmd.Env = append(os.Environ(), "HERDR_PLUGIN_STATE_DIR="+stateDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash script failed: %v\noutput: %s", err, out)
	}
	return string(out)
}
