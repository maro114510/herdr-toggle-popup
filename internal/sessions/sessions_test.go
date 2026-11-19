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
	if strings.Contains(stdout.String(), "other-session") {
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
	tmux("new-session", "-d", "-s", "other-session", "sleep 60")

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

func writeFakeTmux(t *testing.T, dir, body string) {
	t.Helper()
	path := filepath.Join(dir, "tmux")
	script := "#!/bin/sh\nset -eu\n" + body
	//nolint:gosec // The fake tmux must be executable.
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}
