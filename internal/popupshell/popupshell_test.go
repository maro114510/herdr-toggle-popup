package popupshell

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/maro114510/herdr-toggle-popup/internal/config"
)

// Test list:
//
// - workspace scope: execs tmux with a stable session name derived from the namespace hash,
//   workspace:<workspace_id>, and <entrypoint>, starting in the focused pane cwd
// - directory scope: derives the tmux session from namespace:<hash>:directory:<focused cwd>:<entrypoint>
// - tab scope: derives the tmux session from namespace:<hash>:tab:<workspace id>:<focused tab id>:<entrypoint>
// - namespace: the same scope and entrypoint in different Herdr namespaces yield different sessions
// - namespace: an unchanged namespace, scope, and entrypoint reuse the saved session name
// - tmux hides its status line, enables mouse mode, and binds the close key to detach
// - close_key defaults to M-l and an invalid value warns and falls back
// - tmux forces set-clipboard=external in both the create and reattach paths
// - real tmux server: the close key is guarded per session in the root table
// - real tmux server: binding it leaves the default mouse and prefix bindings in place
// - real tmux server: two sessions own different keys and never swallow the other key
// - real tmux server: a changed key applies when the popup is reopened
// - real tmux server: a name tmux rejects falls back to the default
// - real tmux server: the dedicated server reports set-clipboard external for new and existing sessions
// - real tmux server: a pane-originated OSC52 sequence never becomes a tmux buffer
// - real tmux server: new session only on the dedicated server
// - real tmux server: reopening reuses the dedicated session
// - real tmux server: legacy sessions on both servers are not removed
// - real tmux server: a same-name default-server session is not attached to
// - $SHELL unset: defaults the tmux command to /bin/zsh
// - missing tmux: reports a clear error and never execs
// - missing HERDR_SOCKET_PATH: reports a clear error before resolving or execing tmux
// - missing focused cwd: reports a clear error before execing tmux
// - missing tab id (tab scope): reports a clear error before execing tmux

const (
	configDirEnvVar = "HERDR_PLUGIN_CONFIG_DIR"
	contextEnvVar   = "HERDR_PLUGIN_CONTEXT_JSON"
	workspaceID     = "ws1"
	scopeWorkspace  = "workspace"
	focusedCwd      = "/focused/cwd"
	testSocketPath  = "/tmp/herdr-namespace-test.sock"
	testShellPath   = "/opt/homebrew/bin/fish"
	testShellBin    = "/resolved/sh"
	testTmuxBin     = "/resolved/tmux"

	// Real-tmux-server test settings. popupSocketName mirrors the socket in tmuxAttachScript.
	popupSocketName  = "herdr-toggle-popup"
	tmuxSocketDirVar = "TMUX_TMPDIR"
	tmuxClientEnvVar = "TMUX"
	testSessionName  = "herdr-popup-test"
	testCloseKey     = "M-l"
	altCloseKey      = "M-p"

	// Pane-OSC52 probe timings. The pane prints a sentinel after the sequence, so the poll only
	// waits as long as the pane needs to run the probe command.
	osc52ProbeTimeout  = 5 * time.Second
	osc52ProbeInterval = 50 * time.Millisecond
)

// namespacedKey prepends the namespace token derived from testSocketPath to a scope-and-entrypoint
// key, matching the identity tmuxSessionKey builds under the test environment.
func namespacedKey(scopeKey string) string {
	return fmt.Sprintf("namespace:%s:%s", namespaceForSocketPath(testSocketPath), scopeKey)
}

type execCall struct {
	argv0 string
	argv  []string
	envv  []string
}

func setupEnv(t *testing.T) string {
	t.Helper()

	configDir := filepath.Join(t.TempDir(), "plugin-config")
	t.Setenv(configDirEnvVar, configDir)
	t.Setenv(workspaceIDEnvVar, workspaceID)
	t.Setenv(socketPathEnvVar, testSocketPath)
	t.Setenv(contextEnvVar, `{"workspace_id":"ws1","focused_pane_cwd":"/focused/cwd"}`)
	t.Setenv(shellEnvVar, testShellPath)
	return configDir
}

func writeConfig(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func successfulLookPath(t *testing.T) lookPathFunc {
	t.Helper()
	return func(file string) (string, error) {
		switch file {
		case shellBin:
			return testShellBin, nil
		case tmuxBin:
			return testTmuxBin, nil
		case testShellPath:
			return testShellPath, nil
		case defaultShell:
			return defaultShell, nil
		default:
			t.Fatalf("unexpected lookPath(%q)", file)
			return "", nil
		}
	}
}

func wantTmuxWrapperArgv(session string, shell string, closeKey string) []string {
	return []string{
		shellBin, "-c", tmuxAttachScript,
		"popup-shell", session, focusedCwd, shell, testTmuxBin, closeKey,
	}
}

func captureExec(call *execCall) execFunc {
	return func(argv0 string, argv, envv []string) error {
		call.argv0 = argv0
		call.argv = slices.Clone(argv)
		call.envv = slices.Clone(envv)
		return nil
	}
}

// isolatedTmuxEnv points tmux at a private socket directory and drops TMUX.
// The test therefore never touches an ambient server.
func isolatedTmuxEnv(t *testing.T) []string {
	t.Helper()

	// Keep the socket path short: tmux appends "/tmux-<uid>/default" to a 104-byte sun_path limit.
	//nolint:usetesting // t.TempDir embeds the long test name and overflows sun_path.
	dir, err := os.MkdirTemp("", "htp")
	if err != nil {
		t.Fatalf("create tmux socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	env := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, tmuxClientEnvVar+"=") ||
			strings.HasPrefix(entry, tmuxSocketDirVar+"=") {
			continue
		}
		env = append(env, entry)
	}
	return append(env, tmuxSocketDirVar+"="+dir)
}

// runTmux runs tmux against the isolated server and fails the test on a non-zero exit.
func runTmux(t *testing.T, env []string, tmuxPath string, args ...string) string {
	t.Helper()

	//nolint:gosec // The test controls the tmux binary and every argument.
	cmd := exec.Command(tmuxPath, args...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("tmux %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// runAttachScript runs the production attach script against the isolated servers.
// attach-session needs a tty, so the exit code is ignored and only side effects are asserted.
func runAttachScript(t *testing.T, env []string, tmuxPath, session, closeKey string) {
	t.Helper()

	//nolint:gosec // The test drives the production attach script with fixed arguments.
	cmd := exec.Command("sh", "-c", tmuxAttachScript, "popup-shell", session, t.TempDir(), "/bin/sh", tmuxPath, closeKey)
	cmd.Env = env
	_ = cmd.Run()
}

// dedicatedTmux runs tmux against the plugin's dedicated server.
func dedicatedTmux(t *testing.T, env []string, tmuxPath string, args ...string) string {
	t.Helper()

	serverArgs := slices.Concat([]string{"-L", popupSocketName, "-f", "/dev/null"}, args)
	return runTmux(t, env, tmuxPath, serverArgs...)
}

// tmuxSessionExists reports whether session exists on the server selected by serverArgs.
func tmuxSessionExists(t *testing.T, env []string, tmuxPath, session string, serverArgs ...string) bool {
	t.Helper()

	args := append(slices.Clone(serverArgs), "has-session", "-t", session)
	//nolint:gosec // The test controls the tmux binary and every argument.
	cmd := exec.Command(tmuxPath, args...)
	cmd.Env = env
	return cmd.Run() == nil
}

// killTmuxServer tears down the server selected by serverArgs; a missing server is fine.
func killTmuxServer(t *testing.T, env []string, tmuxPath string, serverArgs ...string) {
	t.Helper()

	args := append(slices.Clone(serverArgs), "kill-server")
	//nolint:gosec // The test controls the tmux binary and every argument.
	cmd := exec.Command(tmuxPath, args...)
	cmd.Env = env
	_ = cmd.Run()
}

// isolatedServers tears down both isolated servers the attach script may touch.
func isolatedServers(t *testing.T, env []string, tmuxPath string) {
	t.Helper()

	t.Cleanup(func() {
		killTmuxServer(t, env, tmuxPath, "-L", popupSocketName, "-f", "/dev/null")
		killTmuxServer(t, env, tmuxPath, "-f", "/dev/null")
	})
}

func TestTmuxAttachScriptCreatesNewSessionOnlyOnDedicatedServer(t *testing.T) {
	t.Parallel()

	tmuxPath, err := exec.LookPath(tmuxBin)
	if err != nil {
		t.Skip("tmux not found, skipping real tmux server test")
	}
	env := isolatedTmuxEnv(t)
	isolatedServers(t, env, tmuxPath)

	runAttachScript(t, env, tmuxPath, testSessionName, testCloseKey)

	if !tmuxSessionExists(t, env, tmuxPath, testSessionName, "-L", popupSocketName, "-f", "/dev/null") {
		t.Errorf("session %q was not created on the dedicated server", testSessionName)
	}
	if tmuxSessionExists(t, env, tmuxPath, testSessionName, "-f", "/dev/null") {
		t.Errorf("session %q exists on the default server, want it only on the dedicated server", testSessionName)
	}
}

// TestTmuxAttachScriptLeavesLegacySessionsUntouched covers the pre-namespace identities: a legacy
// session may sit on either the dedicated server (created after the dedicated-server migration) or
// the default server (created before it). Neither may be removed once identity includes the Herdr
// namespace, and the target namespace opens its own dedicated session.
func TestTmuxAttachScriptLeavesLegacySessionsUntouched(t *testing.T) {
	t.Parallel()

	tmuxPath, err := exec.LookPath(tmuxBin)
	if err != nil {
		t.Skip("tmux not found, skipping real tmux server test")
	}
	env := isolatedTmuxEnv(t)
	isolatedServers(t, env, tmuxPath)

	legacySession := sessionName("workspace:ws1:shell")
	namespacedSession := sessionName(namespacedKey("workspace:ws1:shell"))

	runTmux(t, env, tmuxPath, "-L", popupSocketName, "-f", "/dev/null", "new-session", "-d", "-s", legacySession, "-c", t.TempDir(), "/bin/sh")
	runTmux(t, env, tmuxPath, "-f", "/dev/null", "new-session", "-d", "-s", legacySession, "-c", t.TempDir(), "/bin/sh")

	runAttachScript(t, env, tmuxPath, namespacedSession, testCloseKey)

	if !tmuxSessionExists(t, env, tmuxPath, legacySession, "-L", popupSocketName, "-f", "/dev/null") {
		t.Errorf("legacy session %q on the dedicated server was removed", legacySession)
	}
	if !tmuxSessionExists(t, env, tmuxPath, legacySession, "-f", "/dev/null") {
		t.Errorf("legacy session %q on the default server was removed", legacySession)
	}
	if !tmuxSessionExists(t, env, tmuxPath, namespacedSession, "-L", popupSocketName, "-f", "/dev/null") {
		t.Errorf("namespaced session %q was not created on the dedicated server", namespacedSession)
	}
}

// TestTmuxAttachScriptIgnoresDefaultServerSessionWithTargetName guards the removal of the default
// server attach fallback: even when the default server holds a session whose name exactly matches
// the target, the script must create the dedicated session instead of attaching to the default one.
func TestTmuxAttachScriptIgnoresDefaultServerSessionWithTargetName(t *testing.T) {
	t.Parallel()

	tmuxPath, err := exec.LookPath(tmuxBin)
	if err != nil {
		t.Skip("tmux not found, skipping real tmux server test")
	}
	env := isolatedTmuxEnv(t)
	isolatedServers(t, env, tmuxPath)

	target := sessionName(namespacedKey("workspace:ws1:shell"))
	runTmux(t, env, tmuxPath, "-f", "/dev/null", "new-session", "-d", "-s", target, "-c", t.TempDir(), "/bin/sh")

	runAttachScript(t, env, tmuxPath, target, testCloseKey)

	if !tmuxSessionExists(t, env, tmuxPath, target, "-L", popupSocketName, "-f", "/dev/null") {
		t.Errorf("session %q was not created on the dedicated server", target)
	}
	if !tmuxSessionExists(t, env, tmuxPath, target, "-f", "/dev/null") {
		t.Errorf("default-server session %q was removed", target)
	}
}

func TestTmuxAttachScriptKeepsDefaultBindingsOnDedicatedServer(t *testing.T) {
	t.Parallel()

	tmuxPath, err := exec.LookPath(tmuxBin)
	if err != nil {
		t.Skip("tmux not found, skipping real tmux server test")
	}
	env := isolatedTmuxEnv(t)
	isolatedServers(t, env, tmuxPath)

	runAttachScript(t, env, tmuxPath, testSessionName, testCloseKey)

	if got := strings.TrimSpace(dedicatedTmux(t, env, tmuxPath, "show-options", "-t", testSessionName, "status")); got != "status off" {
		t.Errorf("session status = %q, want %q", got, "status off")
	}
	if got := strings.TrimSpace(dedicatedTmux(t, env, tmuxPath, "show-options", "-t", testSessionName, "mouse")); got != "mouse on" {
		t.Errorf("session mouse = %q, want %q", got, "mouse on")
	}

	keys := dedicatedTmux(t, env, tmuxPath, "list-keys", "-T", "root")
	for _, want := range []string{"WheelUpPane", "MouseDrag1Pane", "M-l", "detach-client"} {
		if !strings.Contains(keys, want) {
			t.Errorf("dedicated root key table is missing %q:\n%s", want, keys)
		}
	}
	if got := strings.TrimSpace(dedicatedTmux(t, env, tmuxPath, "show-options", "-t", testSessionName, "@herdr-close-key")); got != "@herdr-close-key "+testCloseKey {
		t.Errorf("session close key = %q, want %q", got, "@herdr-close-key "+testCloseKey)
	}
	assertCloseKeyBinding(t, env, tmuxPath, testCloseKey)
	if got := strings.TrimSpace(dedicatedTmux(t, env, tmuxPath, "show-options", "-g", "prefix")); got != "prefix C-b" {
		t.Errorf("dedicated prefix = %q, want %q", got, "prefix C-b")
	}
}

// assertCloseKeyBinding checks the root binding for key.
// The guard must run detach-client on a hit and send-keys on a miss, in that order.
func assertCloseKeyBinding(t *testing.T, env []string, tmuxPath, key string) {
	t.Helper()

	guard := "#{==:#{@herdr-close-key}," + key + "}"
	rootKeys := dedicatedTmux(t, env, tmuxPath, "list-keys", "-T", "root")
	binding := ""
	for line := range strings.SplitSeq(rootKeys, "\n") {
		if strings.Contains(line, guard) {
			binding = line
			break
		}
	}
	if binding == "" {
		t.Fatalf("root key table has no binding guarded on %q:\n%s", guard, rootKeys)
	}
	detach := strings.Index(binding, "detach-client")
	forward := strings.Index(binding, "send-keys "+key)
	if detach < 0 || forward < 0 || detach > forward {
		t.Errorf("root binding for %q must detach on a guard hit and forward otherwise, got:\n%s", key, binding)
	}
}

// closeKeyGuardHolds evaluates the binding guard in session context.
// The key owns the session only when its @herdr-close-key option equals key.
func closeKeyGuardHolds(t *testing.T, env []string, tmuxPath, session, key string) bool {
	t.Helper()

	format := fmt.Sprintf("#{==:#{@herdr-close-key},%s}", key)
	return strings.TrimSpace(dedicatedTmux(t, env, tmuxPath, "display", "-p", "-t", session, format)) == "1"
}

func sessionCloseKey(t *testing.T, env []string, tmuxPath, session string) string {
	t.Helper()

	return strings.TrimSpace(dedicatedTmux(t, env, tmuxPath, "show-options", "-t", session, "@herdr-close-key"))
}

// TestTmuxAttachScriptScopesCloseKeyToItsSession keeps two sessions on independent close keys.
// Each session owns only its own key and root mouse bindings stay in place.
func TestTmuxAttachScriptScopesCloseKeyToItsSession(t *testing.T) {
	t.Parallel()

	tmuxPath, err := exec.LookPath(tmuxBin)
	if err != nil {
		t.Skip("tmux not found, skipping real tmux server test")
	}
	env := isolatedTmuxEnv(t)
	isolatedServers(t, env, tmuxPath)

	sessionA := sessionName(namespacedKey("workspace:wsA:shell"))
	sessionB := sessionName(namespacedKey("workspace:wsB:shell"))

	runAttachScript(t, env, tmuxPath, sessionA, testCloseKey)
	runAttachScript(t, env, tmuxPath, sessionB, altCloseKey)

	if got := sessionCloseKey(t, env, tmuxPath, sessionA); got != "@herdr-close-key "+testCloseKey {
		t.Errorf("session A close key = %q, want %q", got, "@herdr-close-key "+testCloseKey)
	}
	if got := sessionCloseKey(t, env, tmuxPath, sessionB); got != "@herdr-close-key "+altCloseKey {
		t.Errorf("session B close key = %q, want %q", got, "@herdr-close-key "+altCloseKey)
	}
	assertCloseKeyBinding(t, env, tmuxPath, testCloseKey)
	assertCloseKeyBinding(t, env, tmuxPath, altCloseKey)

	for _, tt := range []struct {
		name    string
		session string
		key     string
		want    bool
	}{
		{"A owns its key", sessionA, testCloseKey, true},
		{"A does not own B's key", sessionA, altCloseKey, false},
		{"B owns its key", sessionB, altCloseKey, true},
		{"B does not own A's key", sessionB, testCloseKey, false},
	} {
		if got := closeKeyGuardHolds(t, env, tmuxPath, tt.session, tt.key); got != tt.want {
			t.Errorf("%s: guard holds = %v, want %v", tt.name, got, tt.want)
		}
	}

	keys := dedicatedTmux(t, env, tmuxPath, "list-keys", "-T", "root")
	for _, want := range []string{"WheelUpPane", "MouseDrag1Pane"} {
		if !strings.Contains(keys, want) {
			t.Errorf("dedicated root key table lost %q:\n%s", want, keys)
		}
	}
}

// TestTmuxAttachScriptAppliesChangedCloseKeyOnReopen rebinds a session on a new key.
// The other session keeps its key and both bindings stay active.
func TestTmuxAttachScriptAppliesChangedCloseKeyOnReopen(t *testing.T) {
	t.Parallel()

	tmuxPath, err := exec.LookPath(tmuxBin)
	if err != nil {
		t.Skip("tmux not found, skipping real tmux server test")
	}
	env := isolatedTmuxEnv(t)
	isolatedServers(t, env, tmuxPath)

	sessionA := sessionName(namespacedKey("workspace:wsA:shell"))
	sessionB := sessionName(namespacedKey("workspace:wsB:shell"))

	runAttachScript(t, env, tmuxPath, sessionA, testCloseKey)
	runAttachScript(t, env, tmuxPath, sessionB, testCloseKey)

	runAttachScript(t, env, tmuxPath, sessionA, altCloseKey)

	if got := sessionCloseKey(t, env, tmuxPath, sessionA); got != "@herdr-close-key "+altCloseKey {
		t.Errorf("session A close key after reopen = %q, want %q", got, "@herdr-close-key "+altCloseKey)
	}
	assertCloseKeyBinding(t, env, tmuxPath, altCloseKey)
	if closeKeyGuardHolds(t, env, tmuxPath, sessionA, testCloseKey) {
		t.Errorf("session A still owns the old key %q after reopening on %q", testCloseKey, altCloseKey)
	}
	if !closeKeyGuardHolds(t, env, tmuxPath, sessionA, altCloseKey) {
		t.Errorf("session A does not own its new key %q", altCloseKey)
	}
	if got := sessionCloseKey(t, env, tmuxPath, sessionB); got != "@herdr-close-key "+testCloseKey {
		t.Errorf("session B close key changed to %q, want it unchanged at %q", got, "@herdr-close-key "+testCloseKey)
	}
	if !closeKeyGuardHolds(t, env, tmuxPath, sessionB, testCloseKey) {
		t.Errorf("session B no longer owns %q after session A changed its key", testCloseKey)
	}
}

// TestTmuxAttachScriptFallsBackWhenTmuxRejectsCloseKey binds the default when tmux rejects a name.
// Alt-l passes the config character check but tmux does not know it.
func TestTmuxAttachScriptFallsBackWhenTmuxRejectsCloseKey(t *testing.T) {
	t.Parallel()

	tmuxPath, err := exec.LookPath(tmuxBin)
	if err != nil {
		t.Skip("tmux not found, skipping real tmux server test")
	}
	env := isolatedTmuxEnv(t)
	isolatedServers(t, env, tmuxPath)

	runAttachScript(t, env, tmuxPath, testSessionName, "Alt-l")

	if got := sessionCloseKey(t, env, tmuxPath, testSessionName); got != "@herdr-close-key "+testCloseKey {
		t.Errorf("session close key = %q, want the fallback %q", got, "@herdr-close-key "+testCloseKey)
	}
	assertCloseKeyBinding(t, env, tmuxPath, testCloseKey)
}

func TestTmuxAttachScriptReusesExistingDedicatedSession(t *testing.T) {
	t.Parallel()

	tmuxPath, err := exec.LookPath(tmuxBin)
	if err != nil {
		t.Skip("tmux not found, skipping real tmux server test")
	}
	env := isolatedTmuxEnv(t)
	isolatedServers(t, env, tmuxPath)

	runAttachScript(t, env, tmuxPath, testSessionName, testCloseKey)
	created := strings.TrimSpace(dedicatedTmux(t, env, tmuxPath, "display", "-p", "-t", testSessionName, "#{session_created}"))

	runAttachScript(t, env, tmuxPath, testSessionName, testCloseKey)
	again := strings.TrimSpace(dedicatedTmux(t, env, tmuxPath, "display", "-p", "-t", testSessionName, "#{session_created}"))

	if created == "" || created != again {
		t.Errorf("session_created changed from %q to %q, want the existing shell session preserved", created, again)
	}
}

// TestTmuxAttachScriptSetsExternalClipboardForNewAndExistingSessions covers both delivery paths of
// the clipboard contract: a freshly created session and a reattach to a saved one must leave the
// dedicated server on set-clipboard=external, so a value changed in between is corrected.
func TestTmuxAttachScriptSetsExternalClipboardForNewAndExistingSessions(t *testing.T) {
	t.Parallel()

	tmuxPath, err := exec.LookPath(tmuxBin)
	if err != nil {
		t.Skip("tmux not found, skipping real tmux server test")
	}
	env := isolatedTmuxEnv(t)
	isolatedServers(t, env, tmuxPath)

	runAttachScript(t, env, tmuxPath, testSessionName, testCloseKey)
	assertDedicatedSetClipboardExternal(t, env, tmuxPath, "new session")

	dedicatedTmux(t, env, tmuxPath, "set-option", "-s", "set-clipboard", "on")
	runAttachScript(t, env, tmuxPath, testSessionName, testCloseKey)
	assertDedicatedSetClipboardExternal(t, env, tmuxPath, "existing session")
}

func assertDedicatedSetClipboardExternal(t *testing.T, env []string, tmuxPath, phase string) {
	t.Helper()

	want := "set-clipboard external"
	if got := strings.TrimSpace(dedicatedTmux(t, env, tmuxPath, "show-options", "-s", "set-clipboard")); got != want {
		t.Errorf("%s: dedicated server set-clipboard = %q, want %q", phase, got, want)
	}
}

// TestTmuxAttachScriptKeepsPaneOSC52OutOfTmuxBuffers proves the security side of
// set-clipboard=external: a sequence emitted by a program inside the pane cannot create a tmux
// buffer. The second phase repeats the probe with set-clipboard=on and expects a buffer, so a
// silent probe cannot pass the first phase by accident.
func TestTmuxAttachScriptKeepsPaneOSC52OutOfTmuxBuffers(t *testing.T) {
	t.Parallel()

	tmuxPath, err := exec.LookPath(tmuxBin)
	if err != nil {
		t.Skip("tmux not found, skipping real tmux server test")
	}
	env := isolatedTmuxEnv(t)
	isolatedServers(t, env, tmuxPath)

	runAttachScript(t, env, tmuxPath, testSessionName, testCloseKey)

	runPaneOSC52Probe(t, env, tmuxPath, "pane-external")
	if got := strings.TrimSpace(dedicatedTmux(t, env, tmuxPath, "list-buffers")); got != "" {
		t.Errorf("set-clipboard=external buffers = %q, want no tmux buffer from a pane-originated OSC52", got)
	}

	dedicatedTmux(t, env, tmuxPath, "set-option", "-s", "set-clipboard", "on")
	runPaneOSC52Probe(t, env, tmuxPath, "pane-on")
	if got := dedicatedTmux(t, env, tmuxPath, "list-buffers"); !strings.Contains(got, "pane-on") {
		t.Errorf("set-clipboard=on buffers = %q, want the pane-originated OSC52 payload %q", got, "pane-on")
	}
}

// runPaneOSC52Probe replaces the session's pane with a command that emits an OSC52 sequence carrying
// payload, then waits for a sentinel printed after the sequence, so tmux has parsed it on return.
// respawn-pane avoids send-keys, whose echoed command line would make the sentinel match too early.
func runPaneOSC52Probe(t *testing.T, env []string, tmuxPath, payload string) {
	t.Helper()

	encoded := base64.StdEncoding.EncodeToString([]byte(payload))
	const sentinel = "osc52-probe-done"
	command := fmt.Sprintf(
		`printf '\033]52;c;%s\007'; printf '%s\n'; sleep 60`,
		encoded, sentinel,
	)
	dedicatedTmux(t, env, tmuxPath, "respawn-pane", "-k", "-t", testSessionName, command)

	deadline := time.Now().Add(osc52ProbeTimeout)
	for {
		if strings.Contains(dedicatedTmux(t, env, tmuxPath, "capture-pane", "-p", "-t", testSessionName), sentinel) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane never printed %q; the OSC52 probe did not run", sentinel)
		}
		time.Sleep(osc52ProbeInterval)
	}
}

//nolint:paralleltest // uses setupEnv, which mutates process env via t.Setenv.
func TestRunExecsTmuxSessionForWorkspaceScope(t *testing.T) {
	setupEnv(t)

	var stderr bytes.Buffer
	var call execCall
	code := run([]string{defaultEntrypoint}, &stderr, successfulLookPath(t), captureExec(&call))

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	wantArgv := wantTmuxWrapperArgv(sessionName(namespacedKey("workspace:ws1:shell")), testShellPath, config.DefaultCloseKey)
	if call.argv0 != testShellBin {
		t.Errorf("argv0 = %q, want %q", call.argv0, testShellBin)
	}
	if !slices.Equal(call.argv, wantArgv) {
		t.Errorf("argv = %v, want %v", call.argv, wantArgv)
	}
	if !slices.Equal(call.envv, os.Environ()) {
		t.Error("envv was not the inherited environment")
	}
}

func TestRunReadsWorkspaceIDFromPluginContextForNativePopup(t *testing.T) {
	setupEnv(t)
	t.Setenv(workspaceIDEnvVar, "")

	var stderr bytes.Buffer
	var call execCall
	code := run([]string{defaultEntrypoint}, &stderr, successfulLookPath(t), captureExec(&call))

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if got := call.argv[4]; got != sessionName(namespacedKey("workspace:ws1:shell")) {
		t.Errorf("session = %q, want workspace session derived from plugin context", got)
	}
}

//nolint:paralleltest // uses setupEnv, which mutates process env via t.Setenv.
func TestRunExecsTmuxSessionForDirectoryScope(t *testing.T) {
	configDir := setupEnv(t)
	writeConfig(t, configDir, "scope = \"directory\"\n")

	var stderr bytes.Buffer
	var call execCall
	code := run([]string{defaultEntrypoint}, &stderr, successfulLookPath(t), captureExec(&call))

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	wantSession := sessionName(namespacedKey("directory:/focused/cwd:shell"))
	if got := call.argv[4]; got != wantSession {
		t.Errorf("session = %q, want %q", got, wantSession)
	}
}

// TestRunUsesConfiguredCloseKey passes close_key to the script and prints no warning.
//
//nolint:paralleltest // uses setupEnv, which mutates process env via t.Setenv.
func TestRunUsesConfiguredCloseKey(t *testing.T) {
	configDir := setupEnv(t)
	writeConfig(t, configDir, "close_key = \""+altCloseKey+"\"\n")

	var stderr bytes.Buffer
	var call execCall
	code := run([]string{defaultEntrypoint}, &stderr, successfulLookPath(t), captureExec(&call))

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if got := call.argv[8]; got != altCloseKey {
		t.Errorf("close key argv = %q, want %q", got, altCloseKey)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty for a valid close key", stderr.String())
	}
}

// TestRunInvalidCloseKeyWarnsAndFallsBack rejects an unsafe key and reports the fallback.
//
//nolint:paralleltest // uses setupEnv, which mutates process env via t.Setenv.
func TestRunInvalidCloseKeyWarnsAndFallsBack(t *testing.T) {
	configDir := setupEnv(t)
	writeConfig(t, configDir, "close_key = \"M-l; touch /tmp/pwned\"\n")

	var stderr bytes.Buffer
	var call execCall
	code := run([]string{defaultEntrypoint}, &stderr, successfulLookPath(t), captureExec(&call))

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "invalid close_key") {
		t.Errorf("stderr = %q, want an invalid close_key warning", stderr.String())
	}
	if got := call.argv[8]; got != config.DefaultCloseKey {
		t.Errorf("close key argv = %q, want the default %q", got, config.DefaultCloseKey)
	}
}

//nolint:paralleltest // uses setupEnv, which mutates process env via t.Setenv.
func TestRunInvalidScopePrintsWarningAndFallsBackToWorkspace(t *testing.T) {
	configDir := setupEnv(t)
	writeConfig(t, configDir, "scope = \"bogus-scope\"\n")

	var stderr bytes.Buffer
	var call execCall
	code := run([]string{defaultEntrypoint}, &stderr, successfulLookPath(t), captureExec(&call))

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "invalid scope") || !strings.Contains(stderr.String(), "bogus-scope") {
		t.Errorf("stderr = %q, want it to contain an invalid scope warning mentioning bogus-scope", stderr.String())
	}
	wantSession := sessionName(namespacedKey("workspace:ws1:shell"))
	if got := call.argv[4]; got != wantSession {
		t.Errorf("session = %q, want %q (fallback to workspace scope)", got, wantSession)
	}
}

//nolint:paralleltest // uses setupEnv, which mutates process env via t.Setenv.
func TestRunValidScopePrintsNoWarning(t *testing.T) {
	configDir := setupEnv(t)
	writeConfig(t, configDir, "scope = \"directory\"\n")

	var stderr bytes.Buffer
	var call execCall
	code := run([]string{defaultEntrypoint}, &stderr, successfulLookPath(t), captureExec(&call))

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty for a valid config", stderr.String())
	}
}

//nolint:paralleltest // uses setupEnv, which mutates process env via t.Setenv.
func TestRunUnsetScopePrintsNoWarning(t *testing.T) {
	setupEnv(t)

	var stderr bytes.Buffer
	var call execCall
	code := run([]string{defaultEntrypoint}, &stderr, successfulLookPath(t), captureExec(&call))

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty when no config file exists", stderr.String())
	}
}

//nolint:paralleltest // uses setupEnv, which mutates process env via t.Setenv.
func TestRunMalformedConfigPrintsWarningAndUsesDefaults(t *testing.T) {
	configDir := setupEnv(t)
	writeConfig(t, configDir, "scope = \"directory\n")

	var stderr bytes.Buffer
	var call execCall
	code := run([]string{defaultEntrypoint}, &stderr, successfulLookPath(t), captureExec(&call))

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "could not parse") {
		t.Errorf("stderr = %q, want a config parse warning", stderr.String())
	}
	wantSession := sessionName(namespacedKey("workspace:ws1:shell"))
	if got := call.argv[4]; got != wantSession {
		t.Errorf("session = %q, want the workspace default %q", got, wantSession)
	}
}

func TestTmuxSessionKeyTable(t *testing.T) {
	const cwdOnlyContext = `{"focused_pane_cwd":"/focused/cwd"}`

	tests := []struct {
		name      string
		scopeMode string
		context   string
		wantKey   string
		wantErr   bool
	}{
		{
			name:      scopeWorkspace,
			scopeMode: scopeWorkspace,
			context:   cwdOnlyContext,
			wantKey:   namespacedKey("workspace:ws1:shell"),
			wantErr:   false,
		},
		{
			name:      scopeDirectory,
			scopeMode: scopeDirectory,
			context:   cwdOnlyContext,
			wantKey:   namespacedKey("directory:/focused/cwd:shell"),
			wantErr:   false,
		},
		{
			name:      scopeTab,
			scopeMode: scopeTab,
			context:   `{"focused_pane_cwd":"/focused/cwd","tab_id":"tab1"}`,
			wantKey:   namespacedKey("tab:ws1:tab1:shell"),
			wantErr:   false,
		},
		{
			name:      "tab missing tab_id",
			scopeMode: scopeTab,
			context:   cwdOnlyContext,
			wantKey:   "",
			wantErr:   true,
		},
	}

	t.Setenv(workspaceIDEnvVar, workspaceID)
	t.Setenv(socketPathEnvVar, testSocketPath)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(contextEnvVar, tt.context)
			key, _, err := tmuxSessionKey(tt.scopeMode, defaultEntrypoint)
			if tt.wantErr {
				if err == nil {
					t.Fatal("err = nil, want an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if key != tt.wantKey {
				t.Errorf("key = %q, want %q", key, tt.wantKey)
			}
		})
	}
}

// TestTmuxSessionKeySeparatesNamespaces proves that identical workspace, tab, and directory
// values in different Herdr namespaces never share a session identity, while the same namespace
// reproduces the same identity for reuse.
func TestTmuxSessionKeySeparatesNamespaces(t *testing.T) {
	const (
		contextWorkspace = `{"workspace_id":"ws1","focused_pane_cwd":"/focused/cwd"}`
		contextTab       = `{"workspace_id":"ws1","focused_pane_cwd":"/focused/cwd","tab_id":"tab1"}`
	)

	t.Setenv(workspaceIDEnvVar, workspaceID)

	for _, tt := range []struct {
		name      string
		scopeMode string
		context   string
	}{
		{name: scopeWorkspace, scopeMode: scopeWorkspace, context: contextWorkspace},
		{name: scopeDirectory, scopeMode: scopeDirectory, context: contextWorkspace},
		{name: scopeTab, scopeMode: scopeTab, context: contextTab},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(contextEnvVar, tt.context)

			t.Setenv(socketPathEnvVar, "/tmp/herdr-a.sock")
			keyA, _, err := tmuxSessionKey(tt.scopeMode, defaultEntrypoint)
			if err != nil {
				t.Fatalf("namespace A: err = %v, want nil", err)
			}

			t.Setenv(socketPathEnvVar, "/tmp/herdr-b.sock")
			keyB, _, err := tmuxSessionKey(tt.scopeMode, defaultEntrypoint)
			if err != nil {
				t.Fatalf("namespace B: err = %v, want nil", err)
			}

			if keyA == keyB {
				t.Errorf("key = %q in both namespaces, want distinct identities for %s scope", keyA, tt.name)
			}

			t.Setenv(socketPathEnvVar, "/tmp/herdr-a.sock")
			keyAgain, _, err := tmuxSessionKey(tt.scopeMode, defaultEntrypoint)
			if err != nil {
				t.Fatalf("namespace A second call: err = %v, want nil", err)
			}
			if keyAgain != keyA {
				t.Errorf("key = %q, want repeats in the same namespace to reproduce %q", keyAgain, keyA)
			}
		})
	}
}

func TestRunExecsTmuxSessionForTabScope(t *testing.T) {
	configDir := setupEnv(t)
	writeConfig(t, configDir, "scope = \"tab\"\n")
	t.Setenv(contextEnvVar, `{"workspace_id":"ws1","focused_pane_cwd":"/focused/cwd","tab_id":"tab1"}`)

	var stderr bytes.Buffer
	var call execCall
	code := run([]string{defaultEntrypoint}, &stderr, successfulLookPath(t), captureExec(&call))

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	wantSession := sessionName(namespacedKey("tab:ws1:tab1:shell"))
	if got := call.argv[4]; got != wantSession {
		t.Errorf("session = %q, want %q", got, wantSession)
	}
}

func TestRunMissingTabIDReportsErrorBeforeExec(t *testing.T) {
	configDir := setupEnv(t)
	writeConfig(t, configDir, "scope = \"tab\"\n")
	t.Setenv(contextEnvVar, `{"workspace_id":"ws1","focused_pane_cwd":"/focused/cwd"}`)

	var stderr bytes.Buffer
	execCalled := false
	code := run([]string{defaultEntrypoint}, &stderr, successfulLookPath(t), func(string, []string, []string) error {
		execCalled = true
		return nil
	})

	if code == 0 {
		t.Fatal("exit code = 0, want non-zero")
	}
	if execCalled {
		t.Error("exec was called despite missing tab id")
	}
	if stderr.Len() == 0 {
		t.Error("stderr is empty, want an error message")
	}
}

func TestRunConfiguresDedicatedTmuxServerBeforeAttaching(t *testing.T) {
	t.Parallel()

	for _, want := range []string{
		"-L herdr-toggle-popup",
		"has-session -t \"$1\"",
		"new-session -d -s \"$1\" -c \"$2\" \"$3\"",
		"set-option -t \"$1\" status off",
		"set-option -t \"$1\" mouse on",
		"key=\"$5\"",
		"set-option -t \"$1\" @herdr-close-key \"$key\"",
		"bind-key -n \"$key\" if-shell -F \"#{==:#{@herdr-close-key},$key}\" \"detach-client\" \"send-keys $key\"",
		"key=M-l",
		"set-option -s set-clipboard external",
		"attach-session -t \"$1\"",
	} {
		if !strings.Contains(tmuxAttachScript, want) {
			t.Fatalf("tmux attach script = %q, want it to contain %q", tmuxAttachScript, want)
		}
	}
}

// TestRunAppliesCloseKeyOnEveryConnection runs the close-key setup after the branch split.
// Moving it into either branch would leave the reattach path unconfigured.
func TestRunAppliesCloseKeyOnEveryConnection(t *testing.T) {
	t.Parallel()

	branchEnd := strings.Index(tmuxAttachScript, "\nfi\n")
	setup := strings.Index(tmuxAttachScript, "key=\"$5\"")
	if branchEnd < 0 || setup < 0 || setup < branchEnd {
		t.Fatalf("close-key setup must run after the create/reattach split on every connection:\n%s", tmuxAttachScript)
	}
	if got := strings.Count(tmuxAttachScript, "key=\"$5\""); got != 1 {
		t.Fatalf("close-key setup runs %d times, want once on the shared path:\n%s", got, tmuxAttachScript)
	}
}

// TestRunFallsBackToDefaultCloseKeyWhenTmuxRejectsIt keeps a rejected name from stranding the popup.
func TestRunFallsBackToDefaultCloseKeyWhenTmuxRejectsIt(t *testing.T) {
	t.Parallel()

	for _, want := range []string{
		"key=M-l",
		"bind-key -n \"$key\" if-shell",
	} {
		if !strings.Contains(tmuxAttachScript, want) {
			t.Fatalf("tmux attach script = %q, want it to contain the fallback %q", tmuxAttachScript, want)
		}
	}
}

// TestRunForcesExternalClipboardInBothAttachPaths pins the option to both branches of the script.
// The create path must set it on the new server, and the reattach path must set it again because a
// server can carry a different value from an earlier version or a manual change.
func TestRunForcesExternalClipboardInBothAttachPaths(t *testing.T) {
	t.Parallel()

	const want = "set-option -s set-clipboard external"
	if got := strings.Count(tmuxAttachScript, want); got != 2 {
		t.Fatalf("tmux attach script sets %q %d times, want once on the reattach path and once on the create path:\n%s", want, got, tmuxAttachScript)
	}
}

func TestRunEnablesTmuxMouseModeBeforeAttaching(t *testing.T) {
	t.Parallel()

	if !strings.Contains(tmuxAttachScript, "set-option -t \"$1\" mouse on") {
		t.Fatalf("tmux attach script = %q, want it to enable mouse mode on the target session", tmuxAttachScript)
	}
}

func TestRunAttachScriptReliesOnDefaultKeyTable(t *testing.T) {
	t.Parallel()

	if strings.Contains(tmuxAttachScript, "key-table") {
		t.Fatalf("tmux attach script = %q, want tmux default bindings instead of a custom key table", tmuxAttachScript)
	}
}

func TestRunDefaultsToZshWhenShellUnset(t *testing.T) {
	setupEnv(t)
	t.Setenv(shellEnvVar, "")

	var stderr bytes.Buffer
	var call execCall
	code := run([]string{defaultEntrypoint}, &stderr, successfulLookPath(t), captureExec(&call))

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if got := call.argv[6]; got != defaultShell {
		t.Errorf("shell argv = %q, want %q", got, defaultShell)
	}
}

//nolint:paralleltest // uses setupEnv, which mutates process env via t.Setenv.
func TestRunTmuxLookPathFailureReportsErrorAndNeverExecs(t *testing.T) {
	setupEnv(t)

	var stderr bytes.Buffer
	execCalled := false
	lookPath := func(file string) (string, error) {
		if file == tmuxBin {
			return "", errors.New("tmux not found")
		}
		return file, nil
	}
	execProcess := func(string, []string, []string) error {
		execCalled = true
		return nil
	}

	code := run([]string{defaultEntrypoint}, &stderr, lookPath, execProcess)

	if code == 0 {
		t.Fatal("exit code = 0, want non-zero")
	}
	if execCalled {
		t.Error("exec was called despite a tmux lookup failure")
	}
	if stderr.Len() == 0 {
		t.Error("stderr is empty, want an error message")
	}
}

//nolint:paralleltest // uses setupEnv, which mutates process env via t.Setenv.
func TestRunExecFailureReportsError(t *testing.T) {
	setupEnv(t)

	var stderr bytes.Buffer
	code := run([]string{defaultEntrypoint}, &stderr, successfulLookPath(t), func(string, []string, []string) error {
		return errors.New("exec failed")
	})

	if code == 0 {
		t.Fatal("exit code = 0, want non-zero")
	}
	if stderr.Len() == 0 {
		t.Error("stderr is empty, want an error message")
	}
}

// The namespace requirement is checked before any tmux lookup or connection, so an unset
// HERDR_SOCKET_PATH must fail without even resolving tmux, let alone execing.
func TestRunMissingSocketPathReportsErrorBeforeExec(t *testing.T) {
	setupEnv(t)
	t.Setenv(socketPathEnvVar, "")

	var stderr bytes.Buffer
	lookPathCalled := false
	lookPath := func(file string) (string, error) {
		lookPathCalled = true
		return file, nil
	}
	execCalled := false
	code := run([]string{defaultEntrypoint}, &stderr, lookPath, func(string, []string, []string) error {
		execCalled = true
		return nil
	})

	if code == 0 {
		t.Fatal("exit code = 0, want non-zero")
	}
	if lookPathCalled {
		t.Error("lookPath was called despite missing HERDR_SOCKET_PATH; the check must precede tmux setup")
	}
	if execCalled {
		t.Error("exec was called despite missing HERDR_SOCKET_PATH")
	}
	if !strings.Contains(stderr.String(), socketPathEnvVar) {
		t.Errorf("stderr = %q, want it to name %s", stderr.String(), socketPathEnvVar)
	}
}

func TestRunMissingFocusedCwdReportsErrorBeforeExec(t *testing.T) {
	setupEnv(t)
	t.Setenv(contextEnvVar, `{"workspace_id":"ws1"}`)

	var stderr bytes.Buffer
	execCalled := false
	code := run([]string{defaultEntrypoint}, &stderr, successfulLookPath(t), func(string, []string, []string) error {
		execCalled = true
		return nil
	})

	if code == 0 {
		t.Fatal("exit code = 0, want non-zero")
	}
	if execCalled {
		t.Error("exec was called despite missing focused cwd")
	}
	if stderr.Len() == 0 {
		t.Error("stderr is empty, want an error message")
	}
}
