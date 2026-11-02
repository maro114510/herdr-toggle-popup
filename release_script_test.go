package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Test list for scripts/release.sh:
// - refuses to release from a branch other than main
// - refuses to release with uncommitted changes

const releaseScript = "scripts/release.sh"

func TestReleaseScriptRefusesToRunOutsideMain(t *testing.T) {
	t.Parallel()

	dir := newReleaseCheckout(t, "feature")

	output, code := runReleaseScript(t, dir)

	if code == 0 {
		t.Fatalf("release.sh exit = 0, want a refusal\n%s", output)
	}
	if want := "Release must run from main, not feature."; !strings.Contains(output, want) {
		t.Fatalf("output = %q, want it to contain %q", output, want)
	}
}

func TestReleaseScriptRefusesDirtyWorkingTree(t *testing.T) {
	t.Parallel()

	dir := newReleaseCheckout(t, "main")
	if err := os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("work in progress\n"), 0o600); err != nil {
		t.Fatalf("write untracked file: %v", err)
	}

	output, code := runReleaseScript(t, dir)

	if code == 0 {
		t.Fatalf("release.sh exit = 0, want a refusal\n%s", output)
	}
	if want := "Working tree must be clean before release."; !strings.Contains(output, want) {
		t.Fatalf("output = %q, want it to contain %q", output, want)
	}
	if want := "untracked.txt"; !strings.Contains(output, want) {
		t.Fatalf("output = %q, want it to list the dirty file %q", output, want)
	}
}

// newReleaseCheckout stages the minimum a release run reads before its guards: a named branch and
// a committed plugin manifest.
func newReleaseCheckout(t *testing.T, branch string) string {
	t.Helper()

	dir := t.TempDir()
	runGit(t, dir, "init", "--initial-branch="+branch)
	if err := os.WriteFile(filepath.Join(dir, "herdr-plugin.toml"), []byte("version = \"1.0.0\"\n"), 0o600); err != nil {
		t.Fatalf("write herdr-plugin.toml: %v", err)
	}
	runGit(t, dir, "add", "herdr-plugin.toml")
	runGit(t, dir, "-c", "user.name=Release Test", "-c", "user.email=release@example.com", "commit", "-m", "init")
	return dir
}

func runReleaseScript(t *testing.T, dir string) (string, int) {
	t.Helper()

	script, err := filepath.Abs(releaseScript)
	if err != nil {
		t.Fatalf("resolve %s: %v", releaseScript, err)
	}
	//nolint:gosec // The script path comes from this test package.
	cmd := exec.Command("sh", script)
	cmd.Dir = dir
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output

	err = cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return output.String(), 0
	case errors.As(err, &exitErr):
		return output.String(), exitErr.ExitCode()
	default:
		t.Fatalf("run %s: %v\n%s", releaseScript, err, output.String())
		return "", 0
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()

	//nolint:gosec // The fixture only runs git against repositories under t.TempDir.
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, output)
	}
}
