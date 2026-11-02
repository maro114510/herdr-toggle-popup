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
	testWidth      = "100%"
	testHeight     = "100%"
)

// Test list:
//
// NewClient
// - resolves the binary from HERDR_BIN_PATH when set
// - falls back to a herdr found on PATH when HERDR_BIN_PATH is unset
//
// PluginPopupOpen
// - success: requests the native popup at the given dimensions and accepts its id-less response
// - custom dimensions: passes width and height through to Herdr's --width and --height
// - non-zero exit: returns an error that includes the captured output
// - timeout: returns a clear timeout error when the herdr subprocess exceeds the command bound
// - malformed response: returns an error
//
// Contract smoke
// - fake Herdr rejects drift from the command argv shapes this plugin assumes

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
	if err := c.PluginPopupOpen(t.Context(), testPluginID, testEntrypoint, testCwd, testWidth, testHeight); err != nil {
		t.Fatalf("PluginPopupOpen() error = %v, want the PATH-resolved herdr", err)
	}
	want := "plugin pane open --plugin maro114510.toggle-popup --entrypoint shell --placement popup --width 100% --height 100% --cwd /focused/cwd --focus\n"
	if got := readLog(t, logPath); got != want {
		t.Errorf("log = %q, want %q", got, want)
	}
}

//nolint:paralleltest // newFakeHerdr mutates HERDR_BIN_PATH via t.Setenv, not parallel-safe.
func TestPluginPopupOpen_Success(t *testing.T) {
	logPath := newFakeHerdr(t)

	c := NewClient()
	if err := c.PluginPopupOpen(t.Context(), testPluginID, testEntrypoint, testCwd, testWidth, testHeight); err != nil {
		t.Fatalf("PluginPopupOpen() error = %v", err)
	}
	want := "plugin pane open --plugin maro114510.toggle-popup --entrypoint shell --placement popup --width 100% --height 100% --cwd /focused/cwd --focus\n"
	if got := readLog(t, logPath); got != want {
		t.Errorf("argv = %q, want %q", got, want)
	}
}

func TestPluginPopupOpen_AppliesDimensions(t *testing.T) {
	logPath := newFakeHerdr(t)
	t.Setenv("FAKE_HERDR_EXPECT_WIDTH", "80%")
	t.Setenv("FAKE_HERDR_EXPECT_HEIGHT", "20")

	c := NewClient()
	if err := c.PluginPopupOpen(t.Context(), testPluginID, testEntrypoint, testCwd, "80%", "20"); err != nil {
		t.Fatalf("PluginPopupOpen() error = %v", err)
	}
	want := "plugin pane open --plugin maro114510.toggle-popup --entrypoint shell --placement popup --width 80% --height 20 --cwd /focused/cwd --focus\n"
	if got := readLog(t, logPath); got != want {
		t.Errorf("argv = %q, want %q", got, want)
	}
}

func TestPluginPopupOpen_MalformedJSON(t *testing.T) {
	newFakeHerdr(t)
	t.Setenv("FAKE_HERDR_OPEN_MALFORMED", "1")

	c := NewClient()
	if err := c.PluginPopupOpen(t.Context(), testPluginID, testEntrypoint, testCwd, testWidth, testHeight); err == nil {
		t.Fatal("PluginPopupOpen() error = nil, want malformed-response error")
	}
}

//nolint:paralleltest // newFakeHerdr mutates HERDR_BIN_PATH via t.Setenv, not parallel-safe.
func TestHerdrCLIContractSmoke_Fake(t *testing.T) {
	logPath := newFakeHerdr(t)

	c := NewClient()
	if err := c.PluginPopupOpen(t.Context(), testPluginID, testEntrypoint, testCwd, testWidth, testHeight); err != nil {
		t.Fatalf("PluginPopupOpen() error = %v", err)
	}

	want := "plugin pane open --plugin maro114510.toggle-popup --entrypoint shell --placement popup --width 100% --height 100% --cwd /focused/cwd --focus\n"
	if got := readLog(t, logPath); got != want {
		t.Errorf("fake Herdr argv log = %q, want %q", got, want)
	}
}

func TestPluginPopupOpen_NonZeroExit(t *testing.T) {
	newFakeHerdr(t)
	t.Setenv("FAKE_HERDR_OPEN_EXIT", "1")

	c := NewClient()
	err := c.PluginPopupOpen(t.Context(), testPluginID, testEntrypoint, testCwd, testWidth, testHeight)
	if err == nil {
		t.Fatal("PluginPopupOpen() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "stub open failure") {
		t.Errorf("error = %v, want it to contain captured output", err)
	}
}

func TestPluginPopupOpen_Timeout(t *testing.T) {
	newFakeHerdr(t)
	t.Setenv(commandTimeoutEnvVar, "50ms")
	t.Setenv("FAKE_HERDR_OPEN_DELAY_SECONDS", "0.2")

	c := NewClient()
	err := c.PluginPopupOpen(t.Context(), testPluginID, testEntrypoint, testCwd, testWidth, testHeight)
	if err == nil {
		t.Fatal("PluginPopupOpen() error = nil, want timeout error")
	}
	for _, want := range []string{"herdr plugin pane open", "context deadline exceeded", "50ms"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to contain %q", err, want)
		}
	}
}
