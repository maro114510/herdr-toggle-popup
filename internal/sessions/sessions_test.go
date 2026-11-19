package sessions

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maro114510/herdr-toggle-popup/internal/popupshell"
)

func TestRunListsIdentifiedPluginSessionsAndUnknownLegacySessions(t *testing.T) {
	binDir := t.TempDir()
	writeFakeTmux(t, binDir, `
case "$*" in
  *"list-sessions"*)
    printf '%b\n' 'herdr-toggle-popup-a\tns-a\tworkspace\tshell' 'other-session\t\t\t' 'herdr-toggle-popup-b\t\t\t'
    ;;
  *"list-clients"*)
    printf '%s\n' 'herdr-toggle-popup-a' 'herdr-toggle-popup-a' 'herdr-toggle-popup-b'
    ;;
esac
`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	var stdout, stderr bytes.Buffer
	if code := Run(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}

	want := "namespace\tscope\tentrypoint\tsession\tclients\n" +
		"ns-a\tworkspace\tshell\therdr-toggle-popup-a\t2\n" +
		"unknown\tunknown\tunknown\therdr-toggle-popup-b\t1\n"
	if stdout.String() != want {
		t.Errorf("stdout = %q, want %q", stdout.String(), want)
	}
	if strings.Contains(stdout.String(), foreignSession) {
		t.Errorf("stdout lists non-plugin session: %q", stdout.String())
	}
}

func TestRunWithNoServerPrintsEmptyListingWithoutCreatingOne(t *testing.T) {
	binDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "created")
	writeFakeTmux(t, binDir, `
case "$*" in
  *new-session*|*start-server*) touch "$MARKER" ;;
esac
case "$*" in
  *list-sessions*) exit 1 ;;
esac
`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MARKER", marker)

	var stdout, stderr bytes.Buffer
	if code := Run(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if got, want := stdout.String(), "namespace\tscope\tentrypoint\tsession\tclients\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("listing created a tmux server")
	}
}

// TestRunReadsOwnershipOptionsFromRealTmux guards the listing format against the tmux that sets
// the options: a fake tmux cannot notice a format that real tmux expands to an empty string.
func TestRunReadsOwnershipOptionsFromRealTmux(t *testing.T) {
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		t.Skipf("tmux was not found on PATH: %v", err)
	}
	// Keep the socket path short: tmux appends "/tmux-<uid>/<socket>" to a 104-byte sun_path limit.
	//nolint:usetesting // t.TempDir embeds the long test name and overflows sun_path.
	socketDir, err := os.MkdirTemp("", "htp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	t.Setenv("TMUX_TMPDIR", socketDir)
	t.Setenv("TMUX", "")

	tmux := func(args ...string) {
		t.Helper()
		full := append([]string{"-L", popupshell.SocketName, "-f", "/dev/null"}, args...)
		//nolint:gosec // The test controls the tmux binary and every argument.
		if out, err := exec.Command(tmuxPath, full...).CombinedOutput(); err != nil {
			t.Fatalf("tmux %v: %v\n%s", full, err, out)
		}
	}
	t.Cleanup(func() {
		//nolint:gosec // The test controls the tmux binary and every argument.
		_ = exec.Command(tmuxPath, "-L", popupshell.SocketName, "-f", "/dev/null", "kill-server").Run()
	})

	const identified = "herdr-toggle-popup-aaaa"
	tmux("new-session", "-d", "-s", identified, "sleep 60")
	tmux("set-option", "-t", identified, popupshell.NamespaceOption, "ns-a")
	tmux("set-option", "-t", identified, popupshell.ScopeOption, "workspace")
	tmux("set-option", "-t", identified, popupshell.EntrypointOption, "shell")
	tmux("new-session", "-d", "-s", "herdr-toggle-popup-bbbb", "sleep 60")
	tmux("new-session", "-d", "-s", foreignSession, "sleep 60")

	var stdout, stderr bytes.Buffer
	if code := Run(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}

	want := "namespace\tscope\tentrypoint\tsession\tclients\n" +
		"ns-a\tworkspace\tshell\therdr-toggle-popup-aaaa\t0\n" +
		"unknown\tunknown\tunknown\therdr-toggle-popup-bbbb\t0\n"
	if stdout.String() != want {
		t.Errorf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestRunWithNoRealServerPrintsEmptyListingWithoutStartingOne(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
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

	var stdout, stderr bytes.Buffer
	if code := Run(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if got, want := stdout.String(), "namespace\tscope\tentrypoint\tsession\tclients\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	// tmux creates its per-user socket directory on any invocation; only a socket inside it
	// means a server was started.
	sockets, err := filepath.Glob(filepath.Join(socketDir, "tmux-*", "*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(sockets) != 0 {
		t.Errorf("listing started a tmux server: %v", sockets)
	}
}

const (
	deleteCmd       = "delete"
	detachedSession = "herdr-toggle-popup-detached"
	foreignSession  = "other-session"
	notPlugin       = "not a plugin session"
)

// fakeSessions is a fake tmux whose dedicated server holds an attached identified session, a
// detached identified session, a legacy session without ownership options, and a foreign session.
// Every tmux call is appended to $LOG, and kill-session records its target in $KILLED.
const fakeSessions = `
printf '%s\n' "$*" >> "$LOG"
case "$*" in
  *list-sessions*)
    printf '%b\n' 'herdr-toggle-popup-attached\tns-a\tworkspace\tshell' 'herdr-toggle-popup-detached\tns-a\ttab\tshell' 'herdr-toggle-popup-legacy\t\t\t' 'other-session\t\t\t'
    ;;
  *list-clients*)
    printf '%s\n' 'herdr-toggle-popup-attached'
    ;;
  *kill-session*)
    printf '%s\n' "$*" >> "$KILLED"
    ;;
esac
`

func useFakeTmux(t *testing.T, body string) (killed string) {
	t.Helper()
	binDir := t.TempDir()
	writeFakeTmux(t, binDir, body)
	workDir := t.TempDir()
	killed = filepath.Join(workDir, "killed")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("LOG", filepath.Join(workDir, "log"))
	t.Setenv("KILLED", killed)
	return killed
}

func readLines(t *testing.T, path string) string {
	t.Helper()
	//nolint:gosec // The test controls the path.
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}

//nolint:paralleltest // uses useFakeTmux, which mutates process env via t.Setenv.
func TestListingNeverKillsAnySession(t *testing.T) {
	killed := useFakeTmux(t, fakeSessions)

	var stdout, stderr bytes.Buffer
	if code := Run(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if got := readLines(t, killed); got != "" {
		t.Errorf("listing killed sessions: %q", got)
	}
}

//nolint:paralleltest // uses useFakeTmux, which mutates process env via t.Setenv.
func TestDeleteKillsOnlyTheSelectedDetachedSession(t *testing.T) {
	killed := useFakeTmux(t, fakeSessions)

	var stdout, stderr bytes.Buffer
	if code := Run([]string{deleteCmd, detachedSession}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}

	if got, want := readLines(t, killed), "-L herdr-toggle-popup -f /dev/null kill-session -t =herdr-toggle-popup-detached\n"; got != want {
		t.Errorf("kill calls = %q, want %q", got, want)
	}
	if got, want := stdout.String(), "deleted\therdr-toggle-popup-detached\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

//nolint:paralleltest // uses useFakeTmux, which mutates process env via t.Setenv.
func TestDeleteKillsEverySelectedSessionAndNoOther(t *testing.T) {
	killed := useFakeTmux(t, fakeSessions)

	var stdout, stderr bytes.Buffer
	args := []string{deleteCmd, forceFlag, "herdr-toggle-popup-attached", detachedSession}
	if code := Run(args, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}

	want := "-L herdr-toggle-popup -f /dev/null kill-session -t =herdr-toggle-popup-attached\n" +
		"-L herdr-toggle-popup -f /dev/null kill-session -t =herdr-toggle-popup-detached\n"
	if got := readLines(t, killed); got != want {
		t.Errorf("kill calls = %q, want %q", got, want)
	}
}

//nolint:paralleltest // uses useFakeTmux, which mutates process env via t.Setenv.
func TestDeleteRejectsTargetsItMayNotRemoveAndKillsNothing(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantInMsg string
	}{
		{"non-plugin session", []string{deleteCmd, foreignSession}, notPlugin},
		{"legacy session with unknown ownership", []string{deleteCmd, "herdr-toggle-popup-legacy"}, "unknown ownership"},
		{"absent session", []string{deleteCmd, "herdr-toggle-popup-missing"}, "not found"},
		{"attached session without force", []string{deleteCmd, "herdr-toggle-popup-attached"}, "attached"},
		{"one rejected target spares the valid one", []string{deleteCmd, detachedSession, foreignSession}, notPlugin},
		{"force never admits a legacy session", []string{deleteCmd, forceFlag, "herdr-toggle-popup-legacy"}, "unknown ownership"},
		{"force never admits a non-plugin session", []string{deleteCmd, forceFlag, foreignSession}, notPlugin},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			killed := useFakeTmux(t, fakeSessions)

			var stdout, stderr bytes.Buffer
			if code := Run(tc.args, &stdout, &stderr); code == 0 {
				t.Fatalf("exit code = 0, stdout = %q", stdout.String())
			}
			if !strings.Contains(stderr.String(), tc.wantInMsg) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tc.wantInMsg)
			}
			if got := readLines(t, killed); got != "" {
				t.Errorf("rejected delete killed sessions: %q", got)
			}
			if stdout.Len() != 0 {
				t.Errorf("rejected delete reported deletions: %q", stdout.String())
			}
		})
	}
}

//nolint:paralleltest // uses useFakeTmux, which mutates process env via t.Setenv.
func TestDeleteRefusesWhenClientCountsCannotBeRead(t *testing.T) {
	killed := useFakeTmux(t, `
printf '%s\n' "$*" >> "$LOG"
case "$*" in
  *list-sessions*) printf '%b\n' 'herdr-toggle-popup-detached\tns-a\ttab\tshell' ;;
  *list-clients*) exit 1 ;;
  *kill-session*) printf '%s\n' "$*" >> "$KILLED" ;;
esac
`)

	var stdout, stderr bytes.Buffer
	if code := Run([]string{deleteCmd, detachedSession}, &stdout, &stderr); code == 0 {
		t.Fatal("delete succeeded although attachment could not be determined")
	}
	if got := readLines(t, killed); got != "" {
		t.Errorf("delete killed sessions: %q", got)
	}
}

//nolint:paralleltest // uses useFakeTmux, which mutates process env via t.Setenv.
func TestDeleteWithoutTargetsOrWithUnknownFlagIsUsageErrorAndKillsNothing(t *testing.T) {
	for _, args := range [][]string{{deleteCmd}, {deleteCmd, forceFlag}, {deleteCmd, "--all"}, {"purge"}, {deleteCmd, "--older-than=1h"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			killed := useFakeTmux(t, fakeSessions)

			var stdout, stderr bytes.Buffer
			if code := Run(args, &stdout, &stderr); code != 1 {
				t.Fatalf("exit code = %d, want 1", code)
			}
			if !strings.Contains(stderr.String(), "usage: toggle-popup sessions") {
				t.Errorf("stderr = %q, want usage", stderr.String())
			}
			if got := readLines(t, killed); got != "" {
				t.Errorf("killed sessions: %q", got)
			}
		})
	}
}

//nolint:paralleltest // uses useFakeTmux, which mutates process env via t.Setenv.
func TestDeleteWithNoServerReportsNotFoundWithoutCreatingOne(t *testing.T) {
	killed := useFakeTmux(t, `
printf '%s\n' "$*" >> "$LOG"
case "$*" in
  *new-session*|*start-server*|*kill-session*) printf '%s\n' "$*" >> "$KILLED" ;;
esac
case "$*" in
  *list-sessions*) exit 1 ;;
esac
`)

	var stdout, stderr bytes.Buffer
	if code := Run([]string{deleteCmd, detachedSession}, &stdout, &stderr); code == 0 {
		t.Fatal("delete succeeded without a server")
	}
	if !strings.Contains(stderr.String(), "not found") {
		t.Errorf("stderr = %q, want not found", stderr.String())
	}
	if got := readLines(t, killed); got != "" {
		t.Errorf("delete touched tmux: %q", got)
	}
}

func TestDeleteRemovesOnlySelectedSessionOnRealTmux(t *testing.T) {
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

	tmux := func(args ...string) string {
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

	// The shorter name is a prefix of the longer one: an unanchored -t would match both.
	for _, name := range []string{"herdr-toggle-popup-aaaa", "herdr-toggle-popup-aaaabbbb"} {
		tmux("new-session", "-d", "-s", name, "sleep 60")
		tmux("set-option", "-t", name, popupshell.NamespaceOption, "ns-a")
		tmux("set-option", "-t", name, popupshell.ScopeOption, "workspace")
		tmux("set-option", "-t", name, popupshell.EntrypointOption, "shell")
	}

	var stdout, stderr bytes.Buffer
	if code := Run([]string{deleteCmd, "herdr-toggle-popup-aaaa"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}

	if got, want := tmux("list-sessions", "-F", "#{session_name}"), "herdr-toggle-popup-aaaabbbb\n"; got != want {
		t.Errorf("remaining sessions = %q, want %q", got, want)
	}
}

func writeFakeTmux(t *testing.T, dir, body string) {
	t.Helper()
	path := filepath.Join(dir, "tmux")
	script := "#!/bin/sh\nset -eu\n" + body
	//nolint:gosec // The fake tmux must be executable.
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}
