package toggle

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maro114510/herdr-toggle-popup/internal/state"
)

// Test list (ported from tests/toggle.bats, which specifies toggle.sh's behavior):
//
// - open-when-empty: opens a popup and registers its pane_id under workspace:<id>:<entrypoint>
// - the open call passes plugin, entrypoint, placement, cwd, focus, but no workspace/target-pane
// - hide-when-open: marks a registered visible popup hidden, then closes its Herdr pane
// - show-when-hidden: opens a fresh Herdr pane backed by the same tmux session and marks it visible
// - stale-pane-id-recovery on the hide path
// - live-pane close failure rolls the hidden flag back to visible and leaves the pane registered
// - open failure: prints to stderr, does not touch the registry, exits non-zero
// - open timeout: prints a clear timeout error, does not touch the registry, exits non-zero
// - missing focused-pane cwd: fails before ever invoking herdr plugin pane open
// - missing workspace id: fails before touching the registry or calling herdr
// - HERDR_BIN_PATH fallback: falls back to a herdr found on PATH when the env var is unset
// - mode=switch (default): another open entrypoint's popup is left untouched
// - mode=force-close: closes other open entrypoints' popups (same scope only) before opening,
//   even if closing the other one fails
// - mode=force-open: another open entrypoint's popup is left untouched
// - same-entrypoint toggle still hides it regardless of mode
// - unknown mode: rejected before touching the registry or calling herdr
// - directory scoping: defaults to workspace, opt-in via config, independent per cwd, shared
//   across workspaces, missing cwd fails before opening, force-close scopes to the directory
// - nested popups: force-open stacks a second entrypoint; hiding the inner one leaves the outer
//   entry and pane untouched
// - popup size: no configured entrypoint issues no resize; configured steps run in order;
//   a different entrypoint's config is not applied; malformed steps are skipped individually;
//   a resize failure never fails toggle or touches the registry; resize is never attempted when
//   opening fails, or on the hide/show paths
// - cross-compat: a registry entry written by bash toggle.sh is toggled correctly

const (
	stateDirEnvVar  = "HERDR_PLUGIN_STATE_DIR"
	configDirEnvVar = "HERDR_PLUGIN_CONFIG_DIR"
	contextEnvVar   = "HERDR_PLUGIN_CONTEXT_JSON"

	testEntrypointShell = "shell"
	testEntrypointGit   = "git"
	testWorkspaceID     = "ws1"
	testOtherWorkspace  = "ws2"
	testCwd             = "/focused/cwd"

	keyWorkspaceShell = "workspace:ws1:shell"
	keyWorkspaceGit   = "workspace:ws1:git"

	testPaneExisting = "pane-existing"
	testPaneA        = "pane-a"
	testPaneB        = "pane-b"
	testPaneOuter    = "pane-outer"

	callPaneMove        = "pane move"
	callPaneZoom        = "pane zoom"
	callPluginPaneClose = "plugin pane close"
	callPluginPaneOpen  = "plugin pane open"

	dirPerm  = 0o750
	filePerm = 0o600
)

type testEnv struct {
	stateDir  string
	configDir string
	logPath   string
}

// setupEnv mirrors tests/toggle.bats' setup(): a fresh state dir, an (initially empty) config
// dir, HERDR_WORKSPACE_ID=ws1, a focused_pane_cwd context, and a fake herdr on HERDR_BIN_PATH.
func setupEnv(t *testing.T) testEnv {
	t.Helper()

	stateDir := filepath.Join(t.TempDir(), "plugin-state")
	configDir := filepath.Join(t.TempDir(), "plugin-config")

	t.Setenv(stateDirEnvVar, stateDir)
	t.Setenv(configDirEnvVar, configDir)
	t.Setenv(workspaceIDEnvVar, testWorkspaceID)
	t.Setenv(contextEnvVar, fmt.Sprintf(`{"workspace_id":%q,"focused_pane_cwd":%q}`, testWorkspaceID, testCwd))

	logPath := newFakeHerdr(t)

	return testEnv{stateDir: stateDir, configDir: configDir, logPath: logPath}
}

func (env testEnv) log(t *testing.T) string {
	t.Helper()
	return readLog(t, env.logPath)
}

func (env testEnv) popupsFileExists() bool {
	_, err := os.Stat(filepath.Join(env.stateDir, "popups.json"))
	return err == nil
}

func (env testEnv) entry(t *testing.T, key string) (state.Entry, bool) {
	t.Helper()
	store := state.NewStore(env.stateDir)
	entry, ok, err := store.Get(key)
	if err != nil {
		t.Fatal(err)
	}
	return entry, ok
}

func (env testEnv) seed(t *testing.T, key, paneID, scope, workspaceID string) {
	t.Helper()
	store := state.NewStore(env.stateDir)
	entry := state.Entry{
		PaneID:          paneID,
		PluginID:        pluginID,
		Entrypoint:      strings.TrimPrefix(key[strings.LastIndex(key, ":")+1:], ""),
		Scope:           scope,
		WorkspaceID:     &workspaceID,
		TabID:           nil,
		CreatedAtUnixMs: 1,
		Hidden:          nil,
	}
	if err := store.Set(key, entry); err != nil {
		t.Fatal(err)
	}
}

// setHidden marks the workspace:ws1:shell entry hidden; every test seeding a pre-hidden entry
// works against that key.
func (env testEnv) setHidden(t *testing.T) {
	t.Helper()
	store := state.NewStore(env.stateDir)
	if err := store.SetHidden(keyWorkspaceShell, true); err != nil {
		t.Fatal(err)
	}
}

func (env testEnv) writeConfig(t *testing.T, content string) {
	t.Helper()
	if err := os.MkdirAll(env.configDir, dirPerm); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(env.configDir, "config.toml"), []byte(content), filePerm); err != nil {
		t.Fatal(err)
	}
}

func writeScopeConfig(t *testing.T, env testEnv, scope string) {
	t.Helper()
	env.writeConfig(t, fmt.Sprintf("scope = %q\n", scope))
}

func writeSizeConfig(t *testing.T, env testEnv, entrypoint, steps string) {
	t.Helper()
	env.writeConfig(t, fmt.Sprintf("popup_size.%s = %q\n", entrypoint, steps))
}

func invoke(args ...string) (code int, stderr string) {
	var outBuf, errBuf bytes.Buffer
	code = Run(args, &outBuf, &errBuf)
	return code, errBuf.String()
}

func assertContainsAll(t *testing.T, got string, wants ...string) {
	t.Helper()

	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("got = %q, want it to contain %q", got, want)
		}
	}
}

func TestOpensNewPopupAndSavesItsPaneID(t *testing.T) {
	env := setupEnv(t)
	t.Setenv("STUB_HERDR_OPEN_PANE_ID", "pane-42")

	code, stderr := invoke(testEntrypointShell)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}

	entry, ok := env.entry(t, keyWorkspaceShell)
	if !ok {
		t.Fatal("entry not found")
	}
	if entry.PaneID != "pane-42" {
		t.Errorf("PaneID = %q, want pane-42", entry.PaneID)
	}
	if entry.PluginID != pluginID {
		t.Errorf("PluginID = %q, want %q", entry.PluginID, pluginID)
	}
	if entry.Entrypoint != testEntrypointShell {
		t.Errorf("Entrypoint = %q, want %q", entry.Entrypoint, testEntrypointShell)
	}
	if entry.Scope != "workspace" {
		t.Errorf("Scope = %q, want workspace", entry.Scope)
	}
	if entry.WorkspaceID == nil || *entry.WorkspaceID != testWorkspaceID {
		t.Errorf("WorkspaceID = %v, want %q", entry.WorkspaceID, testWorkspaceID)
	}

	if strings.Contains(env.log(t), callPluginPaneClose) {
		t.Error("log contains plugin pane close, want none")
	}
}

//nolint:paralleltest // uses setupEnv, which mutates shared env vars via t.Setenv, not parallel-safe.
func TestOpenCallPassesExpectedFlags(t *testing.T) {
	env := setupEnv(t)

	code, stderr := invoke(testEntrypointShell)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}

	var openCall string
	for line := range strings.Lines(env.log(t)) {
		if strings.HasPrefix(line, callPluginPaneOpen) {
			openCall = line
			break
		}
	}
	if openCall == "" {
		t.Fatal("no plugin pane open call found in log")
	}
	for _, want := range []string{
		"--plugin " + pluginID,
		"--entrypoint shell",
		"--placement overlay",
		"--cwd /focused/cwd",
		"--focus",
	} {
		if !strings.Contains(openCall, want) {
			t.Errorf("open call = %q, want it to contain %q", openCall, want)
		}
	}
	for _, unwanted := range []string{"--workspace", "--target-pane"} {
		if strings.Contains(openCall, unwanted) {
			t.Errorf("open call = %q, want it to not contain %q", openCall, unwanted)
		}
	}
}

//nolint:paralleltest // uses setupEnv, which mutates shared env vars via t.Setenv, not parallel-safe.
func TestHidesAlreadyOpenPopup(t *testing.T) {
	env := setupEnv(t)
	env.seed(t, keyWorkspaceShell, testPaneExisting, "workspace", testWorkspaceID)

	code, stderr := invoke(testEntrypointShell)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}

	entry, ok := env.entry(t, keyWorkspaceShell)
	if !ok {
		t.Fatal("entry not found")
	}
	if entry.PaneID != testPaneExisting {
		t.Errorf("PaneID = %q, want pane-existing", entry.PaneID)
	}
	if entry.Hidden == nil || !*entry.Hidden {
		t.Errorf("Hidden = %v, want true", entry.Hidden)
	}

	log := env.log(t)
	if !strings.Contains(log, "plugin pane close pane-existing\n") {
		t.Errorf("log = %q, want a plugin pane close pane-existing call", log)
	}
	for _, unwanted := range []string{callPaneMove, callPaneZoom, callPluginPaneOpen} {
		if strings.Contains(log, unwanted) {
			t.Errorf("log = %q, want it to not contain %q", log, unwanted)
		}
	}
}

func TestReShowsHiddenPopup(t *testing.T) {
	env := setupEnv(t)
	env.seed(t, keyWorkspaceShell, testPaneExisting, "workspace", testWorkspaceID)
	env.setHidden(t)
	t.Setenv("STUB_HERDR_OPEN_PANE_ID", "pane-reopened")

	code, stderr := invoke(testEntrypointShell)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}

	entry, ok := env.entry(t, keyWorkspaceShell)
	if !ok {
		t.Fatal("entry not found")
	}
	if entry.PaneID != "pane-reopened" {
		t.Errorf("PaneID = %q, want pane-reopened", entry.PaneID)
	}
	if entry.Hidden != nil && *entry.Hidden {
		t.Errorf("Hidden = %v, want false", entry.Hidden)
	}

	log := env.log(t)
	if !strings.Contains(log, callPluginPaneOpen) {
		t.Errorf("log = %q, want a plugin pane open call", log)
	}
	for _, unwanted := range []string{callPaneMove, callPluginPaneClose, "plugin pane focus", callPaneZoom, "pane get"} {
		if strings.Contains(log, unwanted) {
			t.Errorf("log = %q, want it to not contain %q", log, unwanted)
		}
	}
}

func TestStalePaneClearsEntryAndOpensFreshOnHidePath(t *testing.T) {
	env := setupEnv(t)
	env.seed(t, keyWorkspaceShell, "pane-stale", "workspace", testWorkspaceID)
	t.Setenv("STUB_HERDR_GET_EXIT", "1")
	t.Setenv("STUB_HERDR_OPEN_PANE_ID", "pane-fresh")

	code, stderr := invoke(testEntrypointShell)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}

	entry, ok := env.entry(t, keyWorkspaceShell)
	if !ok {
		t.Fatal("entry not found")
	}
	if entry.PaneID != "pane-fresh" {
		t.Errorf("PaneID = %q, want pane-fresh", entry.PaneID)
	}
	if entry.Hidden != nil && *entry.Hidden {
		t.Errorf("Hidden = %v, want false", entry.Hidden)
	}

	log := env.log(t)
	if !strings.Contains(log, callPluginPaneOpen) {
		t.Errorf("log = %q, want a plugin pane open call", log)
	}
	for _, unwanted := range []string{callPaneZoom, callPluginPaneClose} {
		if strings.Contains(log, unwanted) {
			t.Errorf("log = %q, want it to not contain %q", log, unwanted)
		}
	}
}

func TestHiddenEntryOpensFreshWithoutStaleCheckOnShowPath(t *testing.T) {
	env := setupEnv(t)
	env.seed(t, keyWorkspaceShell, "pane-stale", "workspace", testWorkspaceID)
	env.setHidden(t)
	t.Setenv("STUB_HERDR_GET_EXIT", "1")
	t.Setenv("STUB_HERDR_OPEN_PANE_ID", "pane-fresh")

	code, stderr := invoke(testEntrypointShell)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}

	entry, ok := env.entry(t, keyWorkspaceShell)
	if !ok {
		t.Fatal("entry not found")
	}
	if entry.PaneID != "pane-fresh" {
		t.Errorf("PaneID = %q, want pane-fresh", entry.PaneID)
	}
	if entry.Hidden != nil && *entry.Hidden {
		t.Errorf("Hidden = %v, want false", entry.Hidden)
	}

	log := env.log(t)
	if !strings.Contains(log, callPluginPaneOpen) {
		t.Errorf("log = %q, want a plugin pane open call", log)
	}
	if strings.Contains(log, "plugin pane focus") {
		t.Errorf("log = %q, want it to not contain plugin pane focus", log)
	}
	if strings.Contains(log, "pane get") {
		t.Errorf("log = %q, want it to not contain pane get for a hidden entry", log)
	}
}

func TestHideCloseFailureRollsBackHiddenFlag(t *testing.T) {
	env := setupEnv(t)
	env.seed(t, keyWorkspaceShell, testPaneExisting, "workspace", testWorkspaceID)
	t.Setenv("STUB_HERDR_CLOSE_EXIT", "1")

	code, stderr := invoke(testEntrypointShell)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	entry, ok := env.entry(t, keyWorkspaceShell)
	if !ok {
		t.Fatal("entry not found")
	}
	if entry.PaneID != testPaneExisting {
		t.Errorf("PaneID = %q, want pane-existing", entry.PaneID)
	}
	if entry.Hidden != nil && *entry.Hidden {
		t.Errorf("Hidden = %v, want false", entry.Hidden)
	}
	assertContainsAll(t, stderr, "could not hide the popup", "stub close failure")

	log := env.log(t)
	if !strings.Contains(log, "plugin pane close pane-existing\n") {
		t.Errorf("log = %q, want a plugin pane close call", log)
	}
	for _, unwanted := range []string{callPaneZoom, callPaneMove, callPluginPaneOpen} {
		if strings.Contains(log, unwanted) {
			t.Errorf("log = %q, want it to not contain %q", log, unwanted)
		}
	}
}

func TestShowOpenFailureLeavesHiddenEntryUnchanged(t *testing.T) {
	env := setupEnv(t)
	env.seed(t, keyWorkspaceShell, testPaneExisting, "workspace", testWorkspaceID)
	env.setHidden(t)
	t.Setenv("STUB_HERDR_OPEN_EXIT", "1")

	code, stderr := invoke(testEntrypointShell)
	if code == 0 {
		t.Fatalf("code = 0, want non-zero (stderr = %q)", stderr)
	}

	entry, ok := env.entry(t, keyWorkspaceShell)
	if !ok {
		t.Fatal("entry not found")
	}
	if entry.Hidden == nil || !*entry.Hidden {
		t.Errorf("Hidden = %v, want true", entry.Hidden)
	}

	log := env.log(t)
	if !strings.Contains(log, callPluginPaneOpen) {
		t.Errorf("log = %q, want a plugin pane open call", log)
	}
	if strings.Contains(log, "plugin pane focus") {
		t.Errorf("log = %q, want it to not contain plugin pane focus", log)
	}
}

func TestOpenFailurePrintsErrorAndDoesNotWriteRegistry(t *testing.T) {
	env := setupEnv(t)
	t.Setenv("STUB_HERDR_OPEN_EXIT", "1")

	code, stderr := invoke(testEntrypointShell)
	if code == 0 {
		t.Fatal("code = 0, want non-zero")
	}
	if stderr == "" {
		t.Error("stderr = empty, want a message")
	}
	if env.popupsFileExists() {
		t.Error("popups.json exists, want none")
	}
}

func TestOpenTimeoutPrintsClearErrorAndDoesNotWriteRegistry(t *testing.T) {
	env := setupEnv(t)
	t.Setenv("HERDR_COMMAND_TIMEOUT", "50ms")
	t.Setenv("STUB_HERDR_OPEN_DELAY_SECONDS", "0.2")

	code, stderr := invoke(testEntrypointShell)
	if code == 0 {
		t.Fatal("code = 0, want non-zero")
	}
	for _, want := range []string{"failed to open popup pane", "context deadline exceeded", "50ms"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, want it to contain %q", stderr, want)
		}
	}
	if env.popupsFileExists() {
		t.Error("popups.json exists, want none")
	}
}

func TestConcurrentSameEntrypointOpenIsSerializedAcrossProcesses(t *testing.T) {
	env := setupEnv(t)
	t.Setenv("STUB_HERDR_OPEN_DELAY_SECONDS", "0.2")

	first := startToggleHelper(t)
	second := startToggleHelper(t)

	if err := first.Wait(); err != nil {
		t.Fatalf("first helper: %v", err)
	}
	if err := second.Wait(); err != nil {
		t.Fatalf("second helper: %v", err)
	}

	if got := strings.Count(env.log(t), callPluginPaneOpen); got != 1 {
		t.Fatalf("plugin pane open calls = %d, want 1; log:\n%s", got, env.log(t))
	}
}

func startToggleHelper(t *testing.T) *exec.Cmd {
	t.Helper()

	//nolint:gosec // test helper intentionally re-execs this trusted test binary.
	cmd := exec.Command(os.Args[0], "-test.run=TestToggle_HelperProcess", "--", testEntrypointShell)
	cmd.Env = append(os.Environ(), "GO_WANT_TOGGLE_HELPER_PROCESS=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return cmd
}

//nolint:paralleltest // helper may os.Exit and is only run in a subprocess.
func TestToggle_HelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_TOGGLE_HELPER_PROCESS") != "1" {
		return
	}

	if len(os.Args) < 2 {
		t.Fatalf("helper args = %v", os.Args)
	}
	code, stderr := invoke(os.Args[len(os.Args)-1])
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	os.Exit(0)
}

func TestMissingCwdFailsWithoutCallingOpen(t *testing.T) {
	env := setupEnv(t)
	t.Setenv(contextEnvVar, fmt.Sprintf(`{"workspace_id":%q}`, testWorkspaceID))

	code, stderr := invoke(testEntrypointShell)
	if code == 0 {
		t.Fatal("code = 0, want non-zero")
	}
	if stderr == "" {
		t.Error("stderr = empty, want a message")
	}
	if env.popupsFileExists() {
		t.Error("popups.json exists, want none")
	}
	if strings.Contains(env.log(t), callPluginPaneOpen) {
		t.Error("log contains plugin pane open, want none")
	}
}

func TestMissingWorkspaceIDFails(t *testing.T) {
	setupEnv(t)
	t.Setenv(workspaceIDEnvVar, "")

	code, stderr := invoke(testEntrypointShell)
	if code == 0 {
		t.Fatal("code = 0, want non-zero")
	}
	if stderr == "" {
		t.Error("stderr = empty, want a message")
	}
}

func TestFallsBackToHerdrOnPath(t *testing.T) {
	env := setupEnv(t)
	dir := filepath.Dir(env.logPath)
	t.Setenv(binPathEnvVar, "")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("STUB_HERDR_OPEN_PANE_ID", "pane-on-path")

	code, stderr := invoke(testEntrypointShell)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}

	entry, ok := env.entry(t, keyWorkspaceShell)
	if !ok {
		t.Fatal("entry not found")
	}
	if entry.PaneID != "pane-on-path" {
		t.Errorf("PaneID = %q, want pane-on-path", entry.PaneID)
	}
}

func TestModeSwitchLeavesOtherEntrypointUntouched(t *testing.T) {
	env := setupEnv(t)
	env.seed(t, keyWorkspaceShell, testPaneA, "workspace", testWorkspaceID)
	t.Setenv("STUB_HERDR_OPEN_PANE_ID", testPaneB)

	code, stderr := invoke(testEntrypointGit)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}

	shell, ok := env.entry(t, keyWorkspaceShell)
	if !ok || shell.PaneID != testPaneA {
		t.Errorf("shell entry = %+v, ok=%v, want pane-a", shell, ok)
	}
	git, ok := env.entry(t, keyWorkspaceGit)
	if !ok || git.PaneID != testPaneB {
		t.Errorf("git entry = %+v, ok=%v, want pane-b", git, ok)
	}
	if strings.Contains(env.log(t), callPluginPaneClose) {
		t.Error("log contains plugin pane close, want none")
	}
}

func TestModeForceCloseClosesOtherEntrypointBeforeOpening(t *testing.T) {
	env := setupEnv(t)
	env.seed(t, keyWorkspaceShell, testPaneA, "workspace", testWorkspaceID)
	t.Setenv("STUB_HERDR_OPEN_PANE_ID", testPaneB)

	code, stderr := invoke(testEntrypointGit, ModeForceClose)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}

	if _, ok := env.entry(t, keyWorkspaceShell); ok {
		t.Error("shell entry still present, want deleted")
	}
	git, ok := env.entry(t, keyWorkspaceGit)
	if !ok || git.PaneID != testPaneB {
		t.Errorf("git entry = %+v, ok=%v, want pane-b", git, ok)
	}
	if !strings.Contains(env.log(t), "plugin pane close pane-a\n") {
		t.Errorf("log = %q, want a plugin pane close pane-a call", env.log(t))
	}
}

func TestModeForceCloseStillOpensEvenIfCloseFails(t *testing.T) {
	env := setupEnv(t)
	env.seed(t, keyWorkspaceShell, testPaneA, "workspace", testWorkspaceID)
	t.Setenv("STUB_HERDR_CLOSE_EXIT", "1")
	t.Setenv("STUB_HERDR_OPEN_PANE_ID", testPaneB)

	code, stderr := invoke(testEntrypointGit, ModeForceClose)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}

	if _, ok := env.entry(t, keyWorkspaceShell); ok {
		t.Error("shell entry still present, want deleted")
	}
	git, ok := env.entry(t, keyWorkspaceGit)
	if !ok || git.PaneID != testPaneB {
		t.Errorf("git entry = %+v, ok=%v, want pane-b", git, ok)
	}
}

func TestModeForceCloseOnlyClosesSameWorkspace(t *testing.T) {
	env := setupEnv(t)
	env.seed(t, keyWorkspaceShell, testPaneA, "workspace", testWorkspaceID)
	env.seed(t, "workspace:ws2:shell", "pane-other-ws", "workspace", testOtherWorkspace)
	t.Setenv("STUB_HERDR_OPEN_PANE_ID", testPaneB)

	code, stderr := invoke(testEntrypointGit, ModeForceClose)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}

	if strings.Contains(env.log(t), "plugin pane close pane-other-ws") {
		t.Error("log contains a close call for the other workspace's popup, want none")
	}
	if _, ok := env.entry(t, "workspace:ws2:shell"); !ok {
		t.Error("other workspace's entry deleted, want untouched")
	}
}

func TestModeForceOpenLeavesOtherEntrypointUntouched(t *testing.T) {
	env := setupEnv(t)
	env.seed(t, keyWorkspaceShell, testPaneA, "workspace", testWorkspaceID)
	t.Setenv("STUB_HERDR_OPEN_PANE_ID", testPaneB)

	code, stderr := invoke(testEntrypointGit, ModeForceOpen)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}

	shell, ok := env.entry(t, keyWorkspaceShell)
	if !ok || shell.PaneID != testPaneA {
		t.Errorf("shell entry = %+v, ok=%v, want pane-a", shell, ok)
	}
	git, ok := env.entry(t, keyWorkspaceGit)
	if !ok || git.PaneID != testPaneB {
		t.Errorf("git entry = %+v, ok=%v, want pane-b", git, ok)
	}
	if strings.Contains(env.log(t), callPluginPaneClose) {
		t.Error("log contains plugin pane close, want none")
	}
}

//nolint:paralleltest // uses setupEnv, which mutates shared env vars via t.Setenv, not parallel-safe.
func TestSameEntrypointToggleHidesRegardlessOfMode(t *testing.T) {
	env := setupEnv(t)
	env.seed(t, keyWorkspaceShell, testPaneExisting, "workspace", testWorkspaceID)

	code, stderr := invoke(testEntrypointShell, ModeForceClose)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}

	entry, ok := env.entry(t, keyWorkspaceShell)
	if !ok || entry.Hidden == nil || !*entry.Hidden {
		t.Errorf("entry = %+v, ok=%v, want hidden=true", entry, ok)
	}

	log := env.log(t)
	if !strings.Contains(log, "plugin pane close pane-existing\n") {
		t.Errorf("log = %q, want a plugin pane close call", log)
	}
	for _, unwanted := range []string{callPaneMove, callPaneZoom, callPluginPaneOpen} {
		if strings.Contains(log, unwanted) {
			t.Errorf("log = %q, want it to not contain %q", log, unwanted)
		}
	}
}

//nolint:paralleltest // uses setupEnv, which mutates shared env vars via t.Setenv, not parallel-safe.
func TestRejectsUnknownMode(t *testing.T) {
	env := setupEnv(t)

	code, stderr := invoke(testEntrypointShell, "bogus-mode")
	if code == 0 {
		t.Fatal("code = 0, want non-zero")
	}
	if stderr == "" {
		t.Error("stderr = empty, want a message")
	}
	if env.popupsFileExists() {
		t.Error("popups.json exists, want none")
	}
	if env.log(t) != "" {
		t.Errorf("log = %q, want empty", env.log(t))
	}
}

func TestScopeDefaultsToWorkspaceWhenConfigDirUnset(t *testing.T) {
	setupEnv(t)
	t.Setenv(configDirEnvVar, "")

	code, stderr := invoke(testEntrypointShell)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
}

//nolint:paralleltest // uses setupEnv, which mutates shared env vars via t.Setenv, not parallel-safe.
func TestScopeDefaultsToWorkspaceWhenConfigFileMissing(t *testing.T) {
	setupEnv(t)

	code, stderr := invoke(testEntrypointShell)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
}

//nolint:paralleltest // uses setupEnv, which mutates shared env vars via t.Setenv, not parallel-safe.
func TestScopeDefaultsToWorkspaceWhenNoScopeKey(t *testing.T) {
	env := setupEnv(t)
	env.writeConfig(t, "other = \"value\"\n")

	code, stderr := invoke(testEntrypointShell)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if _, ok := env.entry(t, keyWorkspaceShell); !ok {
		t.Error("workspace-scoped entry not found")
	}
}

//nolint:paralleltest // uses setupEnv, which mutates shared env vars via t.Setenv, not parallel-safe.
func TestScopeExplicitWorkspaceKeepsWorkspaceScoping(t *testing.T) {
	env := setupEnv(t)
	writeScopeConfig(t, env, "workspace")

	code, stderr := invoke(testEntrypointShell)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if _, ok := env.entry(t, keyWorkspaceShell); !ok {
		t.Error("workspace-scoped entry not found")
	}
}

//nolint:paralleltest // uses setupEnv, which mutates shared env vars via t.Setenv, not parallel-safe.
func TestScopeDirectoryRegistersUnderDirectoryKey(t *testing.T) {
	env := setupEnv(t)
	writeScopeConfig(t, env, "directory")

	code, stderr := invoke(testEntrypointShell)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}

	entry, ok := env.entry(t, "directory:/focused/cwd:shell")
	if !ok {
		t.Fatal("directory-scoped entry not found")
	}
	if entry.Scope != "directory" {
		t.Errorf("Scope = %q, want directory", entry.Scope)
	}
	if _, ok := env.entry(t, keyWorkspaceShell); ok {
		t.Error("workspace-scoped entry found, want none")
	}
}

func TestScopeDirectoryTwoCwdsIndependentEntries(t *testing.T) {
	env := setupEnv(t)
	writeScopeConfig(t, env, "directory")

	t.Setenv(contextEnvVar, `{"workspace_id":"ws1","focused_pane_cwd":"/dir/a"}`)
	t.Setenv("STUB_HERDR_OPEN_PANE_ID", testPaneA)
	if code, stderr := invoke(testEntrypointShell); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}

	t.Setenv(contextEnvVar, `{"workspace_id":"ws1","focused_pane_cwd":"/dir/b"}`)
	t.Setenv("STUB_HERDR_OPEN_PANE_ID", testPaneB)
	if code, stderr := invoke(testEntrypointShell); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}

	a, ok := env.entry(t, "directory:/dir/a:shell")
	if !ok || a.PaneID != testPaneA {
		t.Errorf("entry a = %+v, ok=%v, want pane-a", a, ok)
	}
	b, ok := env.entry(t, "directory:/dir/b:shell")
	if !ok || b.PaneID != testPaneB {
		t.Errorf("entry b = %+v, ok=%v, want pane-b", b, ok)
	}
}

func TestScopeDirectorySharedAcrossWorkspaces(t *testing.T) {
	env := setupEnv(t)
	writeScopeConfig(t, env, "directory")

	t.Setenv(workspaceIDEnvVar, "ws1")
	if code, stderr := invoke(testEntrypointShell); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	before, ok := env.entry(t, "directory:/focused/cwd:shell")
	if !ok {
		t.Fatal("entry not found after first open")
	}

	t.Setenv(workspaceIDEnvVar, "ws2")
	if code, stderr := invoke(testEntrypointShell); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}

	if !strings.Contains(env.log(t), "plugin pane close "+before.PaneID+"\n") {
		t.Errorf("log = %q, want a plugin pane close call", env.log(t))
	}
	if strings.Contains(env.log(t), callPaneMove) {
		t.Error("log contains pane move, want none")
	}

	after, ok := env.entry(t, "directory:/focused/cwd:shell")
	if !ok {
		t.Fatal("entry not found after second toggle")
	}
	if after.PaneID != before.PaneID {
		t.Errorf("PaneID changed from %q to %q, want unchanged", before.PaneID, after.PaneID)
	}
	if after.Hidden == nil || !*after.Hidden {
		t.Errorf("Hidden = %v, want true", after.Hidden)
	}
}

func TestScopeDirectoryMissingCwdFailsBeforeOpen(t *testing.T) {
	env := setupEnv(t)
	writeScopeConfig(t, env, "directory")
	t.Setenv(contextEnvVar, `{"workspace_id":"ws1"}`)

	code, stderr := invoke(testEntrypointShell)
	if code == 0 {
		t.Fatal("code = 0, want non-zero")
	}
	if stderr == "" {
		t.Error("stderr = empty, want a message")
	}
	if env.popupsFileExists() {
		t.Error("popups.json exists, want none")
	}
	if strings.Contains(env.log(t), callPluginPaneOpen) {
		t.Error("log contains plugin pane open, want none")
	}
}

func TestNestedForceOpenStacksWithoutClosingFirst(t *testing.T) {
	env := setupEnv(t)

	t.Setenv("STUB_HERDR_OPEN_PANE_ID", testPaneOuter)
	if code, stderr := invoke(testEntrypointShell, ModeForceOpen); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	outer, ok := env.entry(t, keyWorkspaceShell)
	if !ok || outer.PaneID != testPaneOuter {
		t.Errorf("outer entry = %+v, ok=%v, want pane-outer", outer, ok)
	}

	t.Setenv("STUB_HERDR_OPEN_PANE_ID", "pane-inner")
	if code, stderr := invoke(testEntrypointGit, ModeForceOpen); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}

	outer, ok = env.entry(t, keyWorkspaceShell)
	if !ok || outer.PaneID != testPaneOuter {
		t.Errorf("outer entry = %+v, ok=%v, want pane-outer", outer, ok)
	}
	inner, ok := env.entry(t, keyWorkspaceGit)
	if !ok || inner.PaneID != "pane-inner" {
		t.Errorf("inner entry = %+v, ok=%v, want pane-inner", inner, ok)
	}
	if strings.Contains(env.log(t), callPluginPaneClose) {
		t.Error("log contains plugin pane close, want none")
	}
}

// assertEntryPaneIDAndHidden fails t unless entry (found via ok) has the given pane_id and
// hidden flag.
func assertEntryPaneIDAndHidden(t *testing.T, entry state.Entry, ok bool, wantPaneID string, wantHidden bool) {
	t.Helper()
	hidden := entry.Hidden != nil && *entry.Hidden
	if !ok || entry.PaneID != wantPaneID || hidden != wantHidden {
		t.Errorf("entry = %+v, ok=%v, want PaneID=%q Hidden=%v", entry, ok, wantPaneID, wantHidden)
	}
}

// callSequence reduces a fake-herdr call log to its first three whitespace-separated fields per
// line (e.g. "pane layout --pane"), dropping arguments that vary between runs (pane ids).
func callSequence(log string) []string {
	const fieldsPerCall = 3

	var calls []string
	for _, line := range strings.Split(strings.TrimRight(log, "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= fieldsPerCall {
			calls = append(calls, strings.Join(fields[:fieldsPerCall], " "))
		}
	}
	return calls
}

// assertCallSequence fails t unless log's reduced call sequence (see callSequence) equals want.
func assertCallSequence(t *testing.T, log string, want []string) {
	t.Helper()
	got := callSequence(log)
	if len(got) != len(want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("calls[%d] = %q, want %q", i, got[i], w)
		}
	}
}

func TestNestedHidingInnerLeavesOuterUntouched(t *testing.T) {
	env := setupEnv(t)

	t.Setenv("STUB_HERDR_OPEN_PANE_ID", testPaneOuter)
	if code, stderr := invoke(testEntrypointShell, ModeForceOpen); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	t.Setenv("STUB_HERDR_OPEN_PANE_ID", "pane-inner")
	if code, stderr := invoke(testEntrypointGit, ModeForceOpen); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if code, stderr := invoke(testEntrypointGit, ModeForceOpen); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}

	inner, ok := env.entry(t, keyWorkspaceGit)
	assertEntryPaneIDAndHidden(t, inner, ok, "pane-inner", true)
	outer, ok := env.entry(t, keyWorkspaceShell)
	assertEntryPaneIDAndHidden(t, outer, ok, testPaneOuter, false)

	log := env.log(t)
	if !strings.Contains(log, "plugin pane close pane-inner\n") {
		t.Errorf("log = %q, want a plugin pane close call", log)
	}

	assertCallSequence(t, log, []string{
		callPluginPaneOpen,
		callPluginPaneOpen,
		"pane get pane-inner",
		callPluginPaneClose,
	})
}

func TestModeForceCloseScopesToDirectory(t *testing.T) {
	env := setupEnv(t)
	writeScopeConfig(t, env, "directory")
	env.seed(t, "directory:/focused/cwd:shell", testPaneA, "directory", testWorkspaceID)
	env.seed(t, "directory:/other/cwd:shell", "pane-other-dir", "directory", testWorkspaceID)
	t.Setenv("STUB_HERDR_OPEN_PANE_ID", testPaneB)

	code, stderr := invoke(testEntrypointGit, ModeForceClose)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}

	if _, ok := env.entry(t, "directory:/focused/cwd:shell"); ok {
		t.Error("focused-cwd entry still present, want deleted")
	}
	if _, ok := env.entry(t, "directory:/other/cwd:shell"); !ok {
		t.Error("other-cwd entry deleted, want untouched")
	}

	log := env.log(t)
	if !strings.Contains(log, "plugin pane close pane-a\n") {
		t.Errorf("log = %q, want a close call for pane-a", log)
	}
	if strings.Contains(log, "plugin pane close pane-other-dir") {
		t.Error("log contains a close call for pane-other-dir, want none")
	}
}

func TestPopupSizeNoEntryNoResizeCalls(t *testing.T) {
	env := setupEnv(t)
	t.Setenv("STUB_HERDR_OPEN_PANE_ID", "pane-42")

	code, stderr := invoke(testEntrypointShell)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if strings.Contains(env.log(t), "pane resize") {
		t.Error("log contains pane resize, want none")
	}
}

func TestPopupSizeRunsConfiguredSequence(t *testing.T) {
	env := setupEnv(t)
	writeSizeConfig(t, env, testEntrypointShell, "right:0.5:2 down:0.25:1")
	t.Setenv("STUB_HERDR_OPEN_PANE_ID", "pane-42")

	code, stderr := invoke(testEntrypointShell)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}

	var resizeCalls []string
	for _, line := range strings.Split(strings.TrimRight(env.log(t), "\n"), "\n") {
		if strings.HasPrefix(line, "pane resize") {
			resizeCalls = append(resizeCalls, line)
		}
	}
	want := []string{
		"pane resize --direction right --amount 0.5 --pane pane-42",
		"pane resize --direction right --amount 0.5 --pane pane-42",
		"pane resize --direction down --amount 0.25 --pane pane-42",
	}
	if len(resizeCalls) != len(want) {
		t.Fatalf("resize calls = %v, want %v", resizeCalls, want)
	}
	for i, w := range want {
		if resizeCalls[i] != w {
			t.Errorf("resizeCalls[%d] = %q, want %q", i, resizeCalls[i], w)
		}
	}
}

func TestPopupSizeDifferentEntrypointNotApplied(t *testing.T) {
	env := setupEnv(t)
	writeSizeConfig(t, env, testEntrypointGit, "right:0.5:2")
	t.Setenv("STUB_HERDR_OPEN_PANE_ID", "pane-42")

	code, stderr := invoke(testEntrypointShell)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if strings.Contains(env.log(t), "pane resize") {
		t.Error("log contains pane resize, want none")
	}
}

func TestPopupSizeMalformedStepSkipped(t *testing.T) {
	env := setupEnv(t)
	writeSizeConfig(t, env, testEntrypointShell, "sideways:0.5:2 right:notanumber:2 down:0.5:0 up:0.5:1")
	t.Setenv("STUB_HERDR_OPEN_PANE_ID", "pane-42")

	code, stderr := invoke(testEntrypointShell)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}

	var resizeCalls []string
	for _, line := range strings.Split(strings.TrimRight(env.log(t), "\n"), "\n") {
		if strings.HasPrefix(line, "pane resize") {
			resizeCalls = append(resizeCalls, line)
		}
	}
	want := []string{"pane resize --direction up --amount 0.5 --pane pane-42"}
	if len(resizeCalls) != len(want) || resizeCalls[0] != want[0] {
		t.Errorf("resizeCalls = %v, want %v", resizeCalls, want)
	}
}

func TestPopupSizeResizeFailureDoesNotFailToggle(t *testing.T) {
	env := setupEnv(t)
	writeSizeConfig(t, env, testEntrypointShell, "right:0.5:1")
	t.Setenv("STUB_HERDR_OPEN_PANE_ID", "pane-42")
	t.Setenv("STUB_HERDR_RESIZE_EXIT", "1")

	code, stderr := invoke(testEntrypointShell)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}

	entry, ok := env.entry(t, keyWorkspaceShell)
	if !ok || entry.PaneID != "pane-42" {
		t.Errorf("entry = %+v, ok=%v, want pane-42", entry, ok)
	}
}

func TestPopupSizeNeverAttemptedWhenOpenFails(t *testing.T) {
	env := setupEnv(t)
	writeSizeConfig(t, env, testEntrypointShell, "right:0.5:1")
	t.Setenv("STUB_HERDR_OPEN_EXIT", "1")

	code, _ := invoke(testEntrypointShell)
	if code == 0 {
		t.Fatal("code = 0, want non-zero")
	}
	if strings.Contains(env.log(t), "pane resize") {
		t.Error("log contains pane resize, want none")
	}
}

//nolint:paralleltest // uses setupEnv, which mutates shared env vars via t.Setenv, not parallel-safe.
func TestPopupSizeNeverAttemptedOnHidePath(t *testing.T) {
	env := setupEnv(t)
	writeSizeConfig(t, env, testEntrypointShell, "right:0.5:1")
	env.seed(t, keyWorkspaceShell, testPaneExisting, "workspace", testWorkspaceID)

	code, stderr := invoke(testEntrypointShell)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if strings.Contains(env.log(t), "pane resize") {
		t.Error("log contains pane resize, want none")
	}
}

//nolint:paralleltest // uses setupEnv, which mutates shared env vars via t.Setenv, not parallel-safe.
func TestPopupSizeRunsOnShowPathBecauseHiddenPopupOpensNewHerdrPane(t *testing.T) {
	env := setupEnv(t)
	writeSizeConfig(t, env, testEntrypointShell, "right:0.5:1")
	env.seed(t, keyWorkspaceShell, testPaneExisting, "workspace", testWorkspaceID)
	env.setHidden(t)

	code, stderr := invoke(testEntrypointShell)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(env.log(t), "pane resize --direction right --amount 0.5 --pane") {
		t.Errorf("log = %q, want a pane resize call", env.log(t))
	}
}

// TestBashFixture_TogglesHiddenCorrectly anchors Go/bash cross-compatibility: a registry entry
// written by bash's state_set (see internal/state/testdata/bash_fixture.json for the byte-for-
// byte shape) is toggled correctly by the Go subcommand, and every field bash wrote that the Go
// hide path doesn't touch (plugin_id, entrypoint, scope, workspace_id, tab_id) survives
// unchanged, alongside an unrelated entry.
const bashFixtureCreatedAtUnixMs = 1720000000000

// writeBashFixture plants a popups.json byte-for-byte in the shape bash's state_set/
// state_set_hidden would produce: one workspace-scoped entry (the one this test toggles) and one
// unrelated global-scoped entry that must survive untouched.
func writeBashFixture(t *testing.T, env testEnv) {
	t.Helper()

	if err := os.MkdirAll(env.stateDir, dirPerm); err != nil {
		t.Fatal(err)
	}
	bashFixture := `{"version":1,"popups":{` +
		`"workspace:ws1:shell":{"pane_id":"pane-1","plugin_id":"maro114510.toggle-popup","entrypoint":"shell","scope":"workspace","workspace_id":"ws1","tab_id":null,"created_at_unix_ms":1720000000000},` +
		`"global:shell":{"pane_id":"pane-2","plugin_id":"maro114510.toggle-popup","entrypoint":"shell","scope":"global","workspace_id":null,"tab_id":null,"created_at_unix_ms":1720000001000,"hidden":true}` +
		`}}`
	if err := os.WriteFile(filepath.Join(env.stateDir, "popups.json"), []byte(bashFixture), filePerm); err != nil {
		t.Fatal(err)
	}
}

// assertBashFixtureShellEntry checks every field bash's state_set wrote for
// workspace:ws1:shell, so the Go toggle path is proven not to have clobbered anything besides
// the hidden flag it's responsible for.
func assertBashFixtureShellEntry(t *testing.T, entry state.Entry) {
	t.Helper()

	if entry.PaneID != "pane-1" {
		t.Errorf("PaneID = %q, want pane-1", entry.PaneID)
	}
	if entry.PluginID != pluginID {
		t.Errorf("PluginID = %q, want %q", entry.PluginID, pluginID)
	}
	if entry.Entrypoint != testEntrypointShell {
		t.Errorf("Entrypoint = %q, want %q", entry.Entrypoint, testEntrypointShell)
	}
	if entry.Scope != "workspace" {
		t.Errorf("Scope = %q, want workspace", entry.Scope)
	}
	if entry.WorkspaceID == nil || *entry.WorkspaceID != testWorkspaceID {
		t.Errorf("WorkspaceID = %v, want %q", entry.WorkspaceID, testWorkspaceID)
	}
	if entry.CreatedAtUnixMs != bashFixtureCreatedAtUnixMs {
		t.Errorf("CreatedAtUnixMs = %d, want %d", entry.CreatedAtUnixMs, bashFixtureCreatedAtUnixMs)
	}
}

//nolint:paralleltest // uses setupEnv, which mutates shared env vars via t.Setenv, not parallel-safe.
func TestBashFixture_TogglesHiddenCorrectly(t *testing.T) {
	env := setupEnv(t)
	writeBashFixture(t, env)

	code, stderr := invoke(testEntrypointShell)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}

	entry, ok := env.entry(t, keyWorkspaceShell)
	if !ok {
		t.Fatal("workspace:ws1:shell entry not found")
	}
	assertBashFixtureShellEntry(t, entry)
	assertEntryPaneIDAndHidden(t, entry, ok, "pane-1", true)

	other, ok := env.entry(t, "global:shell")
	assertEntryPaneIDAndHidden(t, other, ok, "pane-2", true)
}
