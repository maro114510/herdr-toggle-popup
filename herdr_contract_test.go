package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Test list for the Herdr contract:
//
// - the linked plugin's manifest action opens the popup, and a second open is refused while it
//   is visible
// - the action receives the invocation context, and the popup starts the saved tmux session in
//   that focused cwd
// - the popup starts at all only when HERDR_SOCKET_PATH and HERDR_WORKSPACE_ID reached it,
//   because the saved-session identity requires both
// - detaching the popup client closes the popup, and the next open resumes the same saved
//   session in the same cwd
//
// The test needs a real Herdr binary named by HERDR_CONTRACT_BIN. HERDR_CONTRACT_REQUIRE=1
// turns the skip into a failure, so the contract CI cannot pass without running it.

const (
	contractBinEnv       = "HERDR_CONTRACT_BIN"
	contractRequireEnv   = "HERDR_CONTRACT_REQUIRE"
	contractTrackEnv     = "HERDR_CONTRACT_TRACK"
	contractLogDirEnv    = "HERDR_CONTRACT_LOG_DIR"
	contractHerdrSession = "herdr-contract"
	contractTmuxSocket   = "herdr-toggle-popup"
	contractEntrypoint   = "shell"
	contractServerLog    = "server.log"
	contractFailureLog   = "failure.txt"
	contractWaitTimeout  = 20 * time.Second
	contractStopTimeout  = 5 * time.Second
	contractPollInterval = 100 * time.Millisecond
	contractFileMode     = 0o600
	contractDirMode      = 0o750
	contractPluginList   = "plugin list"
)

// contract owns the isolated HOME, tmux socket directory, and headless Herdr server the test
// drives. Every path stays short because Herdr puts its API socket under HOME and the Unix
// socket address has little room.
type contract struct {
	t         *testing.T
	bin       string
	tmuxPath  string
	root      string
	home      string
	tmuxDir   string
	workspace string
	logDir    string
	env       []string
	server    *exec.Cmd
}

type workspaceContract struct {
	id  string
	cwd string
}

//nolint:paralleltest // The contract starts an isolated Herdr and tmux server.
func TestHerdrContract(t *testing.T) {
	c := newContract(t)
	c.buildPlugin()
	c.startServer()

	if stdout, stderr, err := c.herdr("plugin", "link", c.root); err != nil {
		t.Fatalf("plugin link: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	workspace := c.createWorkspace()
	pluginID, actionID := c.linkedPlugin()
	c.assertActionContext(actionID, workspace)

	session := c.waitForSession()
	c.assertSessionCwd(session, workspace)
	c.assertPopupOpen(pluginID)

	// Detaching the popup client is the plugin's close path: the popup command exits, Herdr
	// closes the popup, and the saved tmux session keeps running for the next open.
	c.detach(session)
	c.waitForPopupClosed(pluginID)
	c.assertPopupOpen(pluginID)
	c.assertSessionCwd(session, workspace)

	c.detach(session)
	c.waitForPopupClosed(pluginID)
	c.assertPopupOpen(pluginID)
}

func newContract(t *testing.T) *contract {
	t.Helper()

	bin := contractBinary(t)
	tmuxPath := contractTmuxBinary(t)
	root := contractRoot(t)
	dir := contractTempDir(t)

	logDir := os.Getenv(contractLogDirEnv)
	if logDir == "" {
		logDir = filepath.Join(dir, "logs")
	}

	c := &contract{
		t:         t,
		bin:       bin,
		tmuxPath:  tmuxPath,
		root:      root,
		home:      filepath.Join(dir, "home"),
		tmuxDir:   filepath.Join(dir, "tmux"),
		workspace: filepath.Join(dir, "workspace"),
		logDir:    logDir,
		env:       nil,
		server:    nil,
	}
	for _, path := range []string{c.home, c.tmuxDir, c.workspace, c.logDir} {
		//nolint:gosec // Every path is joined under the test's own temporary directory.
		if err := os.MkdirAll(path, contractDirMode); err != nil {
			t.Fatalf("create %s: %v", path, err)
		}
	}
	c.env = contractEnv(c.home, c.tmuxDir, bin)
	return c
}

func contractBinary(t *testing.T) string {
	t.Helper()

	bin := os.Getenv(contractBinEnv)
	if bin == "" {
		if os.Getenv(contractRequireEnv) == "1" {
			t.Fatalf("%s=1, but %s is not set", contractRequireEnv, contractBinEnv)
		}
		t.Skipf("%s is not set; skipping the Herdr contract", contractBinEnv)
	}
	//nolint:gosec // The contract CI supplies this path through HERDR_CONTRACT_BIN.
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("%s %q: %v", contractBinEnv, bin, err)
	}
	return bin
}

func contractTmuxBinary(t *testing.T) string {
	t.Helper()

	path, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatalf("tmux is required for the Herdr contract: %v", err)
	}
	return path
}

func contractRoot(t *testing.T) string {
	t.Helper()

	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "herdr-plugin.toml")); err != nil {
		t.Fatalf("the contract test must run from the repository root: %v", err)
	}
	return root
}

func contractTempDir(t *testing.T) string {
	t.Helper()

	// Keep the path short: Herdr puts its API socket under HOME, and sockaddr_un has little room.
	base := os.TempDir()
	if runtime.GOOS == "darwin" {
		base = "/tmp"
	}
	//nolint:usetesting // t.TempDir embeds the test name and overflows sun_path.
	dir, err := os.MkdirTemp(base, "herdr-contract")
	if err != nil {
		t.Fatalf("create contract directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// contractEnv isolates the server from the caller's Herdr and tmux servers. Every ambient
// HERDR_* value is dropped, because HERDR_SOCKET_PATH would otherwise redirect the server.
func contractEnv(home, tmuxDir, bin string) []string {
	values := []string{
		"HOME=" + home,
		"TMUX_TMPDIR=" + tmuxDir,
		"HERDR_SESSION=" + contractHerdrSession,
		"HERDR_BIN_PATH=" + bin,
		"HERDR_DISABLE_SOUND=1",
		"SHELL=" + contractShell(),
	}
	keys := make(map[string]struct{}, len(values)+1)
	for _, value := range values {
		key, _, _ := strings.Cut(value, "=")
		keys[key] = struct{}{}
	}
	out := make([]string, 0, len(os.Environ())+len(values))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, drop := keys[key]; drop || strings.HasPrefix(key, "HERDR_") || key == "TMUX" {
			continue
		}
		out = append(out, entry)
	}
	return append(out, values...)
}

func contractShell() string {
	for _, candidate := range []string{"/bin/bash", "/bin/sh"} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return "/bin/sh"
}

func (c *contract) buildPlugin() {
	c.t.Helper()

	cmd := exec.Command("sh", "scripts/build.sh")
	cmd.Dir = c.root
	out, err := cmd.CombinedOutput()
	if err != nil {
		c.t.Fatalf("scripts/build.sh: %v\n%s", err, out)
	}
}

func (c *contract) startServer() {
	c.t.Helper()

	logPath := filepath.Join(c.logDir, contractServerLog)
	//nolint:gosec // logPath is built from the test's own log directory.
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, contractFileMode)
	if err != nil {
		c.t.Fatalf("open %s: %v", logPath, err)
	}
	//nolint:gosec // The test runs the Herdr binary under test.
	cmd := exec.Command(c.bin, "server")
	cmd.Env = c.env
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		c.t.Fatalf("start herdr server: %v", err)
	}
	c.server = cmd
	c.t.Cleanup(func() {
		if c.t.Failed() {
			c.dumpFailureLog()
		}
		c.stopServer()
		_ = logFile.Close()
	})
	c.waitForServer(logPath)
}

func (c *contract) waitForServer(logPath string) {
	c.t.Helper()

	deadline := time.Now().Add(contractWaitTimeout)
	for {
		if _, _, err := c.herdr("status", "server"); err == nil {
			return
		}
		if time.Now().After(deadline) {
			c.t.Fatalf("herdr server did not start; see %s", logPath)
		}
		time.Sleep(contractPollInterval)
	}
}

func (c *contract) stopServer() {
	if c.server == nil {
		return
	}
	if stdout, stderr, err := c.tmux("kill-server"); err != nil {
		c.t.Logf("tmux kill-server: %v\n%s%s", err, stdout, stderr)
	}
	if stdout, stderr, err := c.herdr("server", "stop"); err != nil {
		c.t.Logf("herdr server stop: %v\n%s%s", err, stdout, stderr)
	}
	done := make(chan error, 1)
	go func() { done <- c.server.Wait() }()
	select {
	case <-done:
	case <-time.After(contractStopTimeout):
		_ = c.server.Process.Kill()
		<-done
	}
}

func (c *contract) herdr(args ...string) (stdout, stderr string, err error) {
	//nolint:gosec // The test controls the Herdr binary and every argument.
	cmd := exec.Command(c.bin, args...)
	cmd.Env = c.env
	return runCommand(cmd)
}

func (c *contract) tmux(args ...string) (stdout, stderr string, err error) {
	//nolint:gosec // The test controls the tmux binary and every argument.
	cmd := exec.Command(c.tmuxPath, append([]string{"-L", contractTmuxSocket}, args...)...)
	cmd.Env = c.env
	return runCommand(cmd)
}

func runCommand(cmd *exec.Cmd) (stdout, stderr string, err error) {
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	err = cmd.Run()
	return out.String(), errBuf.String(), err
}

func (c *contract) mustTmux(args ...string) string {
	c.t.Helper()

	stdout, stderr, err := c.tmux(args...)
	if err != nil {
		c.t.Fatalf("tmux %v: %v\nstdout:\n%s\nstderr:\n%s", args, err, stdout, stderr)
	}
	return stdout
}

func (c *contract) decodeJSON(target any, args ...string) {
	c.t.Helper()

	stdout, stderr, err := c.herdr(args...)
	if err != nil {
		c.t.Fatalf("herdr %v: %v\nstdout:\n%s\nstderr:\n%s", args, err, stdout, stderr)
	}
	if err := json.Unmarshal([]byte(stdout), target); err != nil {
		c.t.Fatalf("decode herdr %v output: %v\nstdout:\n%s", args, err, stdout)
	}
}

func (c *contract) createWorkspace() workspaceContract {
	c.t.Helper()

	var created struct {
		Result struct {
			Workspace struct {
				WorkspaceID string `json:"workspace_id"`
			} `json:"workspace"`
			RootPane struct {
				Cwd string `json:"cwd"`
			} `json:"root_pane"`
		} `json:"result"`
	}
	c.decodeJSON(&created, "workspace", "create", "--cwd", c.workspace, "--label", "contract", "--no-focus")

	workspace := workspaceContract{
		id:  created.Result.Workspace.WorkspaceID,
		cwd: created.Result.RootPane.Cwd,
	}
	if workspace.id == "" || workspace.cwd == "" {
		c.t.Fatalf("workspace create returned an empty id or cwd: %+v", workspace)
	}
	return workspace
}

func (c *contract) linkedPlugin() (pluginID, actionID string) {
	c.t.Helper()

	var listed struct {
		Result struct {
			Plugins []struct {
				PluginID string `json:"plugin_id"`
				Actions  []struct {
					ID string `json:"id"`
				} `json:"actions"`
			} `json:"plugins"`
		} `json:"result"`
	}
	c.decodeJSON(&listed, "plugin", "list", "--json")
	if len(listed.Result.Plugins) != 1 {
		c.t.Fatalf("%s returned %d plugins, want 1", contractPluginList, len(listed.Result.Plugins))
	}
	plugin := listed.Result.Plugins[0]
	if len(plugin.Actions) == 0 {
		c.t.Fatalf("plugin %q declares no actions", plugin.PluginID)
	}
	return plugin.PluginID, plugin.PluginID + "." + plugin.Actions[0].ID
}

func (c *contract) assertActionContext(actionID string, workspace workspaceContract) {
	c.t.Helper()

	var invoked struct {
		Result struct {
			Context struct {
				FocusedPaneCwd string `json:"focused_pane_cwd"`
				WorkspaceID    string `json:"workspace_id"`
				TabID          string `json:"tab_id"`
			} `json:"context"`
		} `json:"result"`
	}
	c.decodeJSON(&invoked, "plugin", "action", "invoke", actionID)

	context := invoked.Result.Context
	if context.WorkspaceID != workspace.id {
		c.t.Fatalf("action context workspace = %q, want %q", context.WorkspaceID, workspace.id)
	}
	if context.FocusedPaneCwd != workspace.cwd {
		c.t.Fatalf("action context cwd = %q, want %q", context.FocusedPaneCwd, workspace.cwd)
	}
	if context.TabID == "" {
		c.t.Fatal("action context has no tab id")
	}
}

func (c *contract) waitForSession() string {
	c.t.Helper()

	deadline := time.Now().Add(contractWaitTimeout)
	for {
		stdout, _, err := c.tmux("list-sessions", "-F", "#{session_name}")
		if err == nil {
			sessions := nonEmptyLines(stdout)
			if len(sessions) == 1 {
				return sessions[0]
			}
			if len(sessions) > 1 {
				c.t.Fatalf("expected one popup session, found %v", sessions)
			}
		}
		if time.Now().After(deadline) {
			c.t.Fatal("the popup did not create its saved tmux session")
		}
		time.Sleep(contractPollInterval)
	}
}

func (c *contract) assertSessionCwd(session string, workspace workspaceContract) {
	c.t.Helper()

	cwd := strings.TrimSpace(c.mustTmux("display", "-t", session, "-p", "#{pane_current_path}"))
	if cwd != workspace.cwd {
		c.t.Fatalf("saved session cwd = %q, want %q", cwd, workspace.cwd)
	}
	sessions := nonEmptyLines(c.mustTmux("list-sessions", "-F", "#{session_name}"))
	if len(sessions) != 1 || sessions[0] != session {
		c.t.Fatalf("saved session %q disappeared: %v", session, sessions)
	}
}

func (c *contract) assertPopupOpen(pluginID string) {
	c.t.Helper()

	stdout, stderr, err := c.herdr(
		"plugin", "pane", "open",
		"--plugin", pluginID,
		"--entrypoint", contractEntrypoint,
		"--placement", "popup",
	)
	if err == nil {
		c.t.Fatalf("the popup was not visible: a second open returned %s", stdout)
	}
	if !strings.Contains(stdout+stderr, "ui_busy") {
		c.t.Fatalf("expected ui_busy while the popup is visible, got %v:\n%s%s", err, stdout, stderr)
	}
}

func (c *contract) waitForPopupClosed(pluginID string) {
	c.t.Helper()

	deadline := time.Now().Add(contractWaitTimeout)
	for {
		stdout, stderr, err := c.herdr(
			"plugin", "pane", "open",
			"--plugin", pluginID,
			"--entrypoint", contractEntrypoint,
			"--placement", "popup",
		)
		if err == nil {
			return
		}
		if !strings.Contains(stdout+stderr, "ui_busy") {
			c.t.Fatalf("popup open failed while waiting for the popup to close: %v\n%s%s", err, stdout, stderr)
		}
		if time.Now().After(deadline) {
			c.t.Fatal("the popup did not close after its client detached")
		}
		time.Sleep(contractPollInterval)
	}
}

// detach waits for the popup client to attach, then detaches it. The popup command starts
// asynchronously after plugin pane open returns, so the client is not immediately present.
func (c *contract) detach(session string) {
	c.t.Helper()

	deadline := time.Now().Add(contractWaitTimeout)
	for {
		clients, _, err := c.tmux("list-clients", "-t", session, "-F", "#{client_name}")
		if err == nil && len(nonEmptyLines(clients)) > 0 {
			c.mustTmux("detach-client", "-s", session)
			return
		}
		if time.Now().After(deadline) {
			c.t.Fatalf("no popup client attached to %s", session)
		}
		time.Sleep(contractPollInterval)
	}
}

func (c *contract) dumpFailureLog() {
	sections := []struct {
		name string
		args []string
	}{
		{name: "herdr status server", args: []string{"status", "server"}},
		{name: contractPluginList, args: []string{"plugin", "list", "--json"}},
		{name: "herdr plugin log list", args: []string{"plugin", "log", "list", "--limit", "100"}},
	}

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "track=%s\nherdr=%s\n\n", os.Getenv(contractTrackEnv), c.bin)
	for _, section := range sections {
		fmt.Fprintf(&buf, "## %s\n", section.name)
		stdout, stderr, err := c.herdr(section.args...)
		buf.WriteString(stdout)
		buf.WriteString(stderr)
		if err != nil {
			fmt.Fprintf(&buf, "\n(error: %v)\n", err)
		}
		buf.WriteString("\n")
	}
	fmt.Fprintf(&buf, "## tmux sessions\n")
	stdout, stderr, err := c.tmux("list-sessions", "-F", "#{session_name} #{pane_current_path}")
	buf.WriteString(stdout)
	buf.WriteString(stderr)
	if err != nil {
		fmt.Fprintf(&buf, "\n(error: %v)\n", err)
	}

	path := filepath.Join(c.logDir, contractFailureLog)
	if err := os.WriteFile(path, buf.Bytes(), contractFileMode); err != nil {
		c.t.Logf("write %s: %v", path, err)
		return
	}
	c.t.Logf("Herdr contract diagnostics: %s", path)
}

func nonEmptyLines(out string) []string {
	lines := make([]string, 0, strings.Count(out, "\n"))
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
