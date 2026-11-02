package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Test list for scripts/reproducible-build.sh:
// - two independent builds with matching digests and the expected version pass
// - a digest mismatch fails and names the differing asset
// - a binary that does not embed the expected version fails
// - a host binary that reports another version fails
// - missing arguments fail with usage

const fakeReproducibleGo = `#!/bin/sh
out=""
version=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o)
      shift
      out="$1"
      ;;
    -ldflags)
      shift
      version="${1##*-X main.version=}"
      ;;
  esac
  shift
done
reported="${version}"
case "${FAKE_GO_MODE:-}" in
  omit-version)
    version=""
    reported="dev"
    ;;
  report-other)
    reported="9.9.9"
    ;;
  digest-mismatch)
    case "${PWD}" in
      *source-tree-at-a-second-path*) reported="${reported}-second" ;;
    esac
    ;;
esac
{
  printf '#!/bin/sh\n'
  printf 'printf "%%s\n" "%s"\n' "${reported}"
  if [ -n "${version}" ]; then
    printf '# %s\n' "${version}"
  fi
} > "${out}"
chmod +x "${out}"
`

const fakeReproducibleSHA256Sum = `#!/bin/sh
printf '%s  %s\n' "$(cksum "$1" | cut -d' ' -f1)" "$1"
`

func TestReproducibleBuildScriptAcceptsMatchingIndependentBuilds(t *testing.T) {
	t.Parallel()

	pathDir := newReproducibleBuildPath(t)
	outDir := filepath.Join(t.TempDir(), "dist")

	stdout, stderr, err := runReproducibleBuildScript(t, pathDir, outDir)
	if err != nil {
		t.Fatalf("reproducible-build.sh failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	for _, asset := range []string{
		"toggle-popup_darwin_amd64",
		"toggle-popup_darwin_arm64",
		"toggle-popup_linux_amd64",
		"toggle-popup_linux_arm64",
	} {
		if !strings.Contains(stdout, asset) {
			t.Errorf("stdout = %q, want the digest line for %s", stdout, asset)
		}
		if _, statErr := os.Stat(filepath.Join(outDir, asset)); statErr != nil {
			t.Errorf("stat %s: %v", asset, statErr)
		}
	}
}

func TestReproducibleBuildScriptFailsOnDigestMismatch(t *testing.T) {
	t.Parallel()

	pathDir := newReproducibleBuildPath(t)
	outDir := filepath.Join(t.TempDir(), "dist")

	_, stderr, err := runReproducibleBuildScript(t, pathDir, outDir, "FAKE_GO_MODE=digest-mismatch")
	if err == nil {
		t.Fatal("reproducible-build.sh succeeded with differing digests; want failure")
	}
	for _, want := range []string{
		"digest mismatch",
		"toggle-popup_darwin_amd64",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, want it to contain %q", stderr, want)
		}
	}
}

func TestReproducibleBuildScriptFailsWhenVersionIsNotEmbedded(t *testing.T) {
	t.Parallel()

	pathDir := newReproducibleBuildPath(t)
	outDir := filepath.Join(t.TempDir(), "dist")

	_, stderr, err := runReproducibleBuildScript(t, pathDir, outDir, "FAKE_GO_MODE=omit-version")
	if err == nil {
		t.Fatal("reproducible-build.sh succeeded without the expected version; want failure")
	}
	if want := "does not embed version 1.2.3"; !strings.Contains(stderr, want) {
		t.Errorf("stderr = %q, want it to contain %q", stderr, want)
	}
}

func TestReproducibleBuildScriptFailsWhenHostBinaryReportsAnotherVersion(t *testing.T) {
	t.Parallel()

	pathDir := newReproducibleBuildPath(t)
	outDir := filepath.Join(t.TempDir(), "dist")

	_, stderr, err := runReproducibleBuildScript(t, pathDir, outDir, "FAKE_GO_MODE=report-other")
	if err == nil {
		t.Fatal("reproducible-build.sh succeeded with another reported version; want failure")
	}
	if want := "reports version 9.9.9"; !strings.Contains(stderr, want) {
		t.Errorf("stderr = %q, want it to contain %q", stderr, want)
	}
}

func TestReproducibleBuildScriptRejectsMissingArguments(t *testing.T) {
	t.Parallel()

	pathDir := newReproducibleBuildPath(t)

	cmd := exec.Command("sh", "scripts/reproducible-build.sh")
	cmd.Env = append(os.Environ(), "PATH="+pathDir)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("reproducible-build.sh succeeded without arguments; want failure")
	}
	if want := "usage: scripts/reproducible-build.sh <version> <output-dir>"; !strings.Contains(string(output), want) {
		t.Errorf("output = %q, want it to contain %q", output, want)
	}
}

func newReproducibleBuildPath(t *testing.T) string {
	t.Helper()
	pathDir := newToolPath(t, []string{
		"cat", "chmod", "cksum", "cut", "dirname", "git", "grep",
		toolMkdir, "mktemp", "pwd", "rm", "tar", "uname",
	})
	writeFakeTool(t, pathDir, "go", fakeReproducibleGo)
	writeFakeTool(t, pathDir, "sha256sum", fakeReproducibleSHA256Sum)
	return pathDir
}

func runReproducibleBuildScript(t *testing.T, pathDir, outDir string, env ...string) (string, string, error) {
	t.Helper()
	//nolint:gosec // The test runs the repository's script with a path under t.TempDir().
	cmd := exec.Command("sh", "scripts/reproducible-build.sh", "1.2.3", outDir)
	cmd.Env = append(os.Environ(), append([]string{"PATH=" + pathDir}, env...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}
