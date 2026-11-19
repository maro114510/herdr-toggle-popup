package sessions

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maro114510/herdr-toggle-popup/internal/popupshell"
)

// The session names are the plugin's hash of the namespace key for the socket path
// /tmp/herdr-a.sock (and /tmp/herdr-b.sock for the other Herdr instance) in one shell entrypoint.
const (
	socketA            = "/tmp/herdr-a.sock"
	contextWorkspace1  = `{"workspace_id":"ws1","tab_id":"tab1","focused_pane_cwd":"/focused/cwd"}`
	namespaceA         = "fa171728acf77961ffbd5bb13f0993ea"
	workspace1Session  = "herdr-toggle-popup-d31cf1ab5fec6a904ec4c996e7fe1c6b"
	workspace2Session  = "herdr-toggle-popup-29d9e6a026e4e9d79f89c438a2fd6c0e"
	directorySession   = "herdr-toggle-popup-5decd10be6b0d059136003f96c7b6d29"
	tabSession         = "herdr-toggle-popup-43875885137ea6b957ae28cd159f9015"
	otherSocketSession = "herdr-toggle-popup-40143aa8fa48ecdb87f6c45c052313b1"
)

// everySession lists the saved shells of two Herdr instances next to a session this plugin does
// not own, so a reset that deletes by anything but the exact identity removes a visible bystander.
const everySession = "'" + workspace1Session + "\t" + namespaceA + "\tworkspace\tshell' " +
	"'" + workspace2Session + "\t" + namespaceA + "\tworkspace\tshell' " +
	"'" + directorySession + "\t" + namespaceA + "\tdirectory\tshell' " +
	"'" + tabSession + "\t" + namespaceA + "\ttab\tshell' " +
	"'" + otherSocketSession + "\tother\tworkspace\tshell' " +
	"'other-session\t\t\t'"

// resetEnv gives the command the Herdr context of a popup opened in workspace ws1.
func resetEnv(t *testing.T, scope string) {
	t.Helper()
	configDir := t.TempDir()
	if scope != "" {
		if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte("scope = \""+scope+"\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", configDir)
	t.Setenv("HERDR_SOCKET_PATH", socketA)
	t.Setenv("HERDR_WORKSPACE_ID", "ws1")
	t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", contextWorkspace1)
}

// fakeResetTmux fakes a tmux server that lists sessionRows and the clients attached to them, and
// returns the file that records every tmux invocation.
func fakeResetTmux(t *testing.T, sessionRows, clientRows string) string {
	t.Helper()
	binDir := t.TempDir()
	log := filepath.Join(t.TempDir(), "calls")
	writeFakeTmux(t, binDir, `
printf '%s\n' "$*" >> "$TMUX_CALLS"
case "$*" in
  *list-sessions*)
    if [ "$FAKE_NO_SERVER" = 1 ]; then exit 1; fi
    printf '%b\n' `+sessionRows+`
    ;;
  *list-clients*)
    if [ "$FAKE_CLIENTS_FAIL" = 1 ]; then exit 1; fi
    printf '%s\n' `+clientRows+`
    ;;
esac
`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_CALLS", log)
	return log
}

func tmuxCalls(t *testing.T, log string) []string {
	t.Helper()
	raw, err := os.ReadFile(log)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

func killCalls(t *testing.T, log string) []string {
	t.Helper()
	var kills []string
	for _, call := range tmuxCalls(t, log) {
		if strings.Contains(call, "kill-") || strings.Contains(call, "new-session") || strings.Contains(call, "start-server") {
			kills = append(kills, call)
		}
	}
	return kills
}

func wantOneKill(t *testing.T, log, session string) {
	t.Helper()
	want := "-L " + popupshell.SocketName + " -f /dev/null kill-session -t =" + session
	got := killCalls(t, log)
	if len(got) != 1 || got[0] != want {
		t.Errorf("mutating tmux calls = %q, want exactly %q", got, want)
	}
}

func TestResetCurrentDeletesOnlyTheSessionOfTheCurrentWorkspaceContext(t *testing.T) {
	resetEnv(t, "")
	log := fakeResetTmux(t, everySession, "")

	var stdout, stderr bytes.Buffer
	if code := ResetCurrent(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}

	wantOneKill(t, log, workspace1Session)
	if !strings.Contains(stdout.String(), workspace1Session) {
		t.Errorf("stdout = %q, want it to name the deleted session %q", stdout.String(), workspace1Session)
	}
}

func TestResetCurrentDeletesTheSessionOfTheConfiguredScope(t *testing.T) {
	tests := map[string]string{
		"directory": directorySession,
		"tab":       tabSession,
	}
	for scope, want := range tests {
		t.Run(scope, func(t *testing.T) {
			resetEnv(t, scope)
			log := fakeResetTmux(t, everySession, "")

			var stdout, stderr bytes.Buffer
			if code := ResetCurrent(nil, &stdout, &stderr); code != 0 {
				t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
			}

			wantOneKill(t, log, want)
		})
	}
}

func TestResetCurrentDeletesNothingWhenTheContextCannotBeResolved(t *testing.T) {
	tests := map[string]func(t *testing.T){
		"missing Herdr socket path": func(t *testing.T) { t.Setenv("HERDR_SOCKET_PATH", "") },
		"missing focused pane cwd": func(t *testing.T) {
			t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", `{"workspace_id":"ws1"}`)
		},
		"missing workspace id": func(t *testing.T) {
			t.Setenv("HERDR_WORKSPACE_ID", "")
			t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", `{"focused_pane_cwd":"/focused/cwd"}`)
		},
	}
	for name, breakContext := range tests {
		t.Run(name, func(t *testing.T) {
			resetEnv(t, "")
			breakContext(t)
			log := fakeResetTmux(t, everySession, "")

			var stdout, stderr bytes.Buffer
			code := ResetCurrent(nil, &stdout, &stderr)

			if code == 0 {
				t.Errorf("exit code = 0, want non-zero; stdout = %q", stdout.String())
			}
			if stderr.Len() == 0 {
				t.Error("stderr is empty, want the reason the context is insufficient")
			}
			if got := tmuxCalls(t, log); len(got) != 0 {
				t.Errorf("tmux calls = %q, want none", got)
			}
		})
	}
}

func TestResetCurrentDeletesNothingWhenTheCurrentSessionIsNotSaved(t *testing.T) {
	resetEnv(t, "")
	others := "'" + workspace2Session + "\t" + namespaceA + "\tworkspace\tshell' " +
		"'" + otherSocketSession + "\tother\tworkspace\tshell' 'other-session\t\t\t'"
	log := fakeResetTmux(t, others, "")

	var stdout, stderr bytes.Buffer
	if code := ResetCurrent(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}

	if got := killCalls(t, log); len(got) != 0 {
		t.Errorf("mutating tmux calls = %q, want none", got)
	}
	if !strings.Contains(stdout.String(), "nothing") {
		t.Errorf("stdout = %q, want it to say nothing was deleted", stdout.String())
	}
}

func TestResetCurrentWithNoServerDeletesNothingAndStartsNone(t *testing.T) {
	resetEnv(t, "")
	log := fakeResetTmux(t, everySession, "")
	t.Setenv("FAKE_NO_SERVER", "1")

	var stdout, stderr bytes.Buffer
	if code := ResetCurrent(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}

	if got := killCalls(t, log); len(got) != 0 {
		t.Errorf("mutating tmux calls = %q, want none", got)
	}
}

func TestResetCurrentRefusesAnAttachedSessionAndDeletesNothing(t *testing.T) {
	resetEnv(t, "")
	log := fakeResetTmux(t, everySession, "'"+workspace1Session+"' '"+workspace1Session+"' '"+workspace2Session+"'")

	var stdout, stderr bytes.Buffer
	code := ResetCurrent(nil, &stdout, &stderr)

	if code == 0 {
		t.Errorf("exit code = 0, want non-zero; stdout = %q", stdout.String())
	}
	if got := killCalls(t, log); len(got) != 0 {
		t.Errorf("mutating tmux calls = %q, want none", got)
	}
	for _, want := range []string{workspace1Session, "2 attached client", "--force"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
		}
	}
}

func TestResetCurrentRefusesWhenAttachedClientsCannotBeCounted(t *testing.T) {
	resetEnv(t, "")
	log := fakeResetTmux(t, everySession, "")
	t.Setenv("FAKE_CLIENTS_FAIL", "1")

	var stdout, stderr bytes.Buffer
	code := ResetCurrent(nil, &stdout, &stderr)

	if code == 0 {
		t.Errorf("exit code = 0, want non-zero; stdout = %q", stdout.String())
	}
	if got := killCalls(t, log); len(got) != 0 {
		t.Errorf("mutating tmux calls = %q, want none", got)
	}
}

func TestResetCurrentWithForceDeletesOnlyTheAttachedCurrentSession(t *testing.T) {
	resetEnv(t, "")
	log := fakeResetTmux(t, everySession, "'"+workspace1Session+"' '"+workspace1Session+"' '"+workspace2Session+"'")

	var stdout, stderr bytes.Buffer
	if code := ResetCurrent([]string{"--force"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}

	wantOneKill(t, log, workspace1Session)
	if !strings.Contains(stdout.String(), "2 attached client") {
		t.Errorf("stdout = %q, want it to report the 2 clients that were detached", stdout.String())
	}
}

func TestResetCurrentRejectsUnknownArguments(t *testing.T) {
	for _, args := range [][]string{{"--forced"}, {"extra"}, {"--force", "extra"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			resetEnv(t, "")
			log := fakeResetTmux(t, everySession, "")

			var stdout, stderr bytes.Buffer
			code := ResetCurrent(args, &stdout, &stderr)

			if code == 0 {
				t.Errorf("exit code = 0, want non-zero")
			}
			if !strings.Contains(stderr.String(), "usage: toggle-popup reset-current [--force]") {
				t.Errorf("stderr = %q, want the usage line", stderr.String())
			}
			if got := tmuxCalls(t, log); len(got) != 0 {
				t.Errorf("tmux calls = %q, want none", got)
			}
		})
	}
}

// realResetServer starts a tmux server on the plugin's socket in an isolated directory with the
// current workspace session, another workspace's session, and a session the plugin does not own.
func realResetServer(t *testing.T) (tmuxPath string, tmux func(args ...string) string) {
	t.Helper()
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		t.Skipf("tmux was not found on PATH: %v", err)
	}
	//nolint:usetesting // t.TempDir embeds the long test name and overflows sun_path.
	socketDir, err := os.MkdirTemp("", "htp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	t.Setenv("TMUX_TMPDIR", socketDir)
	t.Setenv("TMUX", "")

	tmux = func(args ...string) string {
		t.Helper()
		full := append([]string{"-L", popupshell.SocketName, "-f", "/dev/null"}, args...)
		//nolint:gosec // The test controls the tmux binary and every argument.
		out, err := exec.Command(tmuxPath, full...).CombinedOutput()
		if err != nil {
			t.Fatalf("tmux %v: %v\n%s", full, err, out)
		}
		return string(out)
	}
	t.Cleanup(func() {
		//nolint:gosec // The test controls the tmux binary and every argument.
		_ = exec.Command(tmuxPath, "-L", popupshell.SocketName, "-f", "/dev/null", "kill-server").Run()
	})
	for _, name := range []string{workspace1Session, workspace2Session, "other-session"} {
		tmux("new-session", "-d", "-s", name, "sleep 60")
	}
	return tmuxPath, tmux
}

func listedSessions(tmux func(args ...string) string) string {
	return tmux("list-sessions", "-F", "#{session_name}")
}

func TestResetCurrentOnRealTmuxKeepsEveryOtherSession(t *testing.T) {
	_, tmux := realResetServer(t)
	resetEnv(t, "")

	var stdout, stderr bytes.Buffer
	if code := ResetCurrent(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}

	want := workspace2Session + "\nother-session\n"
	if got := listedSessions(tmux); got != want {
		t.Errorf("sessions after reset = %q, want %q", got, want)
	}
}

func TestResetCurrentOnRealTmuxRefusesAnAttachedSessionUnlessForced(t *testing.T) {
	tmuxPath, tmux := realResetServer(t)
	resetEnv(t, "")
	// A control-mode client needs no terminal and still counts as an attached client.
	//nolint:gosec // The test controls the tmux binary and every argument.
	client := exec.Command(tmuxPath, "-L", popupshell.SocketName, "-f", "/dev/null", "-C", "attach-session", "-t", workspace1Session)
	stdin, err := client.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = client.Process.Kill()
		_ = client.Wait()
	})
	waitForClient(t, tmux)
	all := workspace1Session + "\n" + workspace2Session + "\nother-session\n"

	var stdout, stderr bytes.Buffer
	if code := ResetCurrent(nil, &stdout, &stderr); code == 0 {
		t.Fatalf("exit code = 0 for an attached session, stdout = %q", stdout.String())
	}
	if got := listedSessions(tmux); got != all {
		t.Errorf("sessions after a refused reset = %q, want %q", got, all)
	}

	stdout.Reset()
	stderr.Reset()
	if code := ResetCurrent([]string{"--force"}, &stdout, &stderr); code != 0 {
		t.Fatalf("forced exit code = %d, stderr = %q", code, stderr.String())
	}
	want := workspace2Session + "\nother-session\n"
	if got := listedSessions(tmux); got != want {
		t.Errorf("sessions after a forced reset = %q, want %q", got, want)
	}
}

func waitForClient(t *testing.T, tmux func(args ...string) string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.TrimSpace(tmux("list-clients", "-F", "#{client_session}")) != "" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the control-mode client never attached")
}
