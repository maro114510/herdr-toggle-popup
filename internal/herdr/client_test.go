package herdr

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testPluginID   = "maro114510.toggle-popup"
	testEntrypoint = "shell"
	testCwd        = "/focused/cwd"
	testPaneID     = "pane-1"
)

// Test list:
//
// NewClient
// - resolves the binary from HERDR_BIN_PATH when set
// - falls back to a herdr found on PATH when HERDR_BIN_PATH is unset
//
// PluginPaneOpen
// - success: passes plugin, entrypoint, placement, cwd, focus (no workspace/target-pane) and returns pane_id
// - non-zero exit: returns an error that includes the captured output
// - timeout: returns a clear timeout error when the herdr subprocess exceeds the command bound
// - malformed JSON on a zero exit: returns an error
// - missing pane_id on a zero exit: returns an error
//
// PaneExists
// - true on zero exit, argv is "pane get <id>"
// - false on non-zero exit
//
// TabSibling
// - returns the first pane_id that isn't self
// - returns empty when the pane is alone in its tab
// - returns empty on non-zero exit
// - returns empty on malformed JSON
//
// ZoomOn
// - nil on zero exit, argv is "pane zoom <id> --on"
// - error on non-zero exit
//
// PluginPaneFocus
// - nil on zero exit, argv is "plugin pane focus <id>"
// - error on non-zero exit
//
// PaneResize
// - nil on zero exit, argv is "pane resize --direction D --amount A --pane <id>"
// - error on non-zero exit
//
// PluginPaneClose
// - nil on zero exit, argv is "plugin pane close <id>"
// - error on non-zero exit

func newFakeHerdrOnPath(t *testing.T) string {
	t.Helper()

	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found, skipping fake-herdr test")
	}

	dir := t.TempDir()
	bin := filepath.Join(dir, "herdr")
	if err := os.WriteFile(bin, []byte(fakeHerdrScript), fakeHerdrExecPerm); err != nil {
		t.Fatal(err)
	}

	logPath := filepath.Join(dir, "calls.log")
	if err := os.WriteFile(logPath, nil, fakeHerdrLogPerm); err != nil {
		t.Fatal(err)
	}

	t.Setenv(binPathEnvVar, "")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(fakeHerdrLogEnv, logPath)

	return logPath
}

func TestNewClient_UsesBinPathEnv(t *testing.T) {
	t.Setenv(binPathEnvVar, "/some/custom/herdr")

	c := NewClient()
	if c.bin != "/some/custom/herdr" {
		t.Errorf("bin = %q, want %q", c.bin, "/some/custom/herdr")
	}
}

//nolint:paralleltest // newFakeHerdrOnPath mutates PATH/HERDR_BIN_PATH via t.Setenv, not parallel-safe.
func TestNewClient_FallsBackToPathHerdr(t *testing.T) {
	logPath := newFakeHerdrOnPath(t)

	c := NewClient()
	if !c.PaneExists(t.Context(), testPaneID) {
		t.Fatal("PaneExists() = false, want true via PATH-resolved herdr")
	}
	if got := readLog(t, logPath); got != "pane get pane-1\n" {
		t.Errorf("log = %q, want %q", got, "pane get pane-1\n")
	}
}

func TestPluginPaneOpen_Success(t *testing.T) {
	logPath := newFakeHerdr(t)
	t.Setenv("FAKE_HERDR_OPEN_PANE_ID", "pane-42")

	c := NewClient()
	paneID, err := c.PluginPaneOpen(t.Context(), testPluginID, testEntrypoint, testCwd)
	if err != nil {
		t.Fatalf("PluginPaneOpen() error = %v", err)
	}
	if paneID != "pane-42" {
		t.Errorf("paneID = %q, want %q", paneID, "pane-42")
	}

	log := readLog(t, logPath)
	wantArgv := "plugin pane open --plugin maro114510.toggle-popup --entrypoint shell --placement overlay --cwd /focused/cwd --focus\n"
	if log != wantArgv {
		t.Errorf("argv = %q, want %q", log, wantArgv)
	}
}

func TestPluginPaneOpen_NonZeroExit(t *testing.T) {
	newFakeHerdr(t)
	t.Setenv("FAKE_HERDR_OPEN_EXIT", "1")

	c := NewClient()
	_, err := c.PluginPaneOpen(t.Context(), testPluginID, testEntrypoint, testCwd)
	if err == nil {
		t.Fatal("PluginPaneOpen() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "stub open failure") {
		t.Errorf("error = %v, want it to contain captured output", err)
	}
}

func TestPluginPaneOpen_Timeout(t *testing.T) {
	newFakeHerdr(t)
	t.Setenv(commandTimeoutEnvVar, "50ms")
	t.Setenv("FAKE_HERDR_OPEN_DELAY_SECONDS", "0.2")

	c := NewClient()
	_, err := c.PluginPaneOpen(t.Context(), testPluginID, testEntrypoint, testCwd)
	if err == nil {
		t.Fatal("PluginPaneOpen() error = nil, want timeout error")
	}
	for _, want := range []string{"herdr plugin pane open", "context deadline exceeded", "50ms"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to contain %q", err, want)
		}
	}
}

func TestPluginPaneOpen_MalformedJSON(t *testing.T) {
	newFakeHerdr(t)
	t.Setenv("FAKE_HERDR_OPEN_MALFORMED", "1")

	c := NewClient()
	_, err := c.PluginPaneOpen(t.Context(), testPluginID, testEntrypoint, testCwd)
	if err == nil {
		t.Fatal("PluginPaneOpen() error = nil, want error on malformed JSON")
	}
}

func TestPluginPaneOpen_MissingPaneID(t *testing.T) {
	newFakeHerdr(t)
	t.Setenv("FAKE_HERDR_OPEN_MISSING_PANE_ID", "1")

	c := NewClient()
	_, err := c.PluginPaneOpen(t.Context(), testPluginID, testEntrypoint, testCwd)
	if err == nil {
		t.Fatal("PluginPaneOpen() error = nil, want error on missing pane_id")
	}
}

//nolint:paralleltest // newFakeHerdr mutates HERDR_BIN_PATH via t.Setenv, not parallel-safe.
func TestPaneExists_True(t *testing.T) {
	logPath := newFakeHerdr(t)

	c := NewClient()
	if !c.PaneExists(t.Context(), testPaneID) {
		t.Error("PaneExists() = false, want true")
	}
	if got := readLog(t, logPath); got != "pane get pane-1\n" {
		t.Errorf("argv = %q, want %q", got, "pane get pane-1\n")
	}
}

func TestPaneExists_False(t *testing.T) {
	newFakeHerdr(t)
	t.Setenv("FAKE_HERDR_GET_EXIT", "1")

	c := NewClient()
	if c.PaneExists(t.Context(), testPaneID) {
		t.Error("PaneExists() = true, want false on non-zero exit")
	}
}

//nolint:paralleltest // newFakeHerdr mutates HERDR_BIN_PATH via t.Setenv, not parallel-safe.
func TestTabSibling_ReturnsSibling(t *testing.T) {
	logPath := newFakeHerdr(t)

	c := NewClient()
	sibling := c.TabSibling(t.Context(), testPaneID)
	if sibling != "pane-sibling" {
		t.Errorf("TabSibling() = %q, want %q", sibling, "pane-sibling")
	}
	if got := readLog(t, logPath); got != "pane layout --pane pane-1\n" {
		t.Errorf("argv = %q, want %q", got, "pane layout --pane pane-1\n")
	}
}

func TestTabSibling_Solo(t *testing.T) {
	newFakeHerdr(t)
	t.Setenv("FAKE_HERDR_LAYOUT_SOLO", "1")

	c := NewClient()
	if sibling := c.TabSibling(t.Context(), testPaneID); sibling != "" {
		t.Errorf("TabSibling() = %q, want empty when alone in its tab", sibling)
	}
}

func TestTabSibling_Error(t *testing.T) {
	newFakeHerdr(t)
	t.Setenv("FAKE_HERDR_LAYOUT_EXIT", "1")

	c := NewClient()
	if sibling := c.TabSibling(t.Context(), testPaneID); sibling != "" {
		t.Errorf("TabSibling() = %q, want empty on error", sibling)
	}
}

func TestTabSibling_MalformedJSON(t *testing.T) {
	newFakeHerdr(t)
	t.Setenv("FAKE_HERDR_LAYOUT_MALFORMED", "1")

	c := NewClient()
	if sibling := c.TabSibling(t.Context(), testPaneID); sibling != "" {
		t.Errorf("TabSibling() = %q, want empty on malformed JSON", sibling)
	}
}

//nolint:paralleltest // newFakeHerdr mutates HERDR_BIN_PATH via t.Setenv, not parallel-safe.
func TestZoomOn_Success(t *testing.T) {
	logPath := newFakeHerdr(t)

	c := NewClient()
	if err := c.ZoomOn(t.Context(), testPaneID); err != nil {
		t.Fatalf("ZoomOn() error = %v", err)
	}
	if got := readLog(t, logPath); got != "pane zoom pane-1 --on\n" {
		t.Errorf("argv = %q, want %q", got, "pane zoom pane-1 --on\n")
	}
}

func TestZoomOn_Failure(t *testing.T) {
	newFakeHerdr(t)
	t.Setenv("FAKE_HERDR_ZOOM_EXIT", "1")

	c := NewClient()
	if err := c.ZoomOn(t.Context(), testPaneID); err == nil {
		t.Fatal("ZoomOn() error = nil, want error on non-zero exit")
	}
}

//nolint:paralleltest // newFakeHerdr mutates HERDR_BIN_PATH via t.Setenv, not parallel-safe.
func TestPluginPaneFocus_Success(t *testing.T) {
	logPath := newFakeHerdr(t)

	c := NewClient()
	if err := c.PluginPaneFocus(t.Context(), testPaneID); err != nil {
		t.Fatalf("PluginPaneFocus() error = %v", err)
	}
	if got := readLog(t, logPath); got != "plugin pane focus pane-1\n" {
		t.Errorf("argv = %q, want %q", got, "plugin pane focus pane-1\n")
	}
}

func TestPluginPaneFocus_Failure(t *testing.T) {
	newFakeHerdr(t)
	t.Setenv("FAKE_HERDR_FOCUS_EXIT", "1")

	c := NewClient()
	if err := c.PluginPaneFocus(t.Context(), testPaneID); err == nil {
		t.Fatal("PluginPaneFocus() error = nil, want error on non-zero exit")
	}
}

//nolint:paralleltest // newFakeHerdr mutates HERDR_BIN_PATH via t.Setenv, not parallel-safe.
func TestPaneResize_Success(t *testing.T) {
	logPath := newFakeHerdr(t)

	c := NewClient()
	if err := c.PaneResize(t.Context(), testPaneID, "right", "0.5"); err != nil {
		t.Fatalf("PaneResize() error = %v", err)
	}
	want := "pane resize --direction right --amount 0.5 --pane pane-1\n"
	if got := readLog(t, logPath); got != want {
		t.Errorf("argv = %q, want %q", got, want)
	}
}

func TestPaneResize_Failure(t *testing.T) {
	newFakeHerdr(t)
	t.Setenv("FAKE_HERDR_RESIZE_EXIT", "1")

	c := NewClient()
	if err := c.PaneResize(t.Context(), testPaneID, "right", "0.5"); err == nil {
		t.Fatal("PaneResize() error = nil, want error on non-zero exit")
	}
}

//nolint:paralleltest // newFakeHerdr mutates HERDR_BIN_PATH via t.Setenv, not parallel-safe.
func TestPluginPaneClose_Success(t *testing.T) {
	logPath := newFakeHerdr(t)

	c := NewClient()
	if err := c.PluginPaneClose(t.Context(), testPaneID); err != nil {
		t.Fatalf("PluginPaneClose() error = %v", err)
	}
	if got := readLog(t, logPath); got != "plugin pane close pane-1\n" {
		t.Errorf("argv = %q, want %q", got, "plugin pane close pane-1\n")
	}
}

func TestPluginPaneClose_Failure(t *testing.T) {
	newFakeHerdr(t)
	t.Setenv("FAKE_HERDR_CLOSE_EXIT", "1")

	c := NewClient()
	if err := c.PluginPaneClose(t.Context(), testPaneID); err == nil {
		t.Fatal("PluginPaneClose() error = nil, want error on non-zero exit")
	}
}
