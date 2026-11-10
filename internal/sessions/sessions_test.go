package sessions

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func writeFakeTmux(t *testing.T, dir, body string) {
	t.Helper()
	path := filepath.Join(dir, "tmux")
	script := "#!/bin/sh\nset -eu\n" + body
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}
