package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const (
	toolCat     = "cat"
	toolDirname = "dirname"
	toolPwd     = "pwd"

	goosDarwin = "darwin"
	goarchARM  = "arm64"
)

// Test list for scripts/build.sh:
// - missing tmux fails with manual install guidance
// - missing tmux never invokes package managers even when they are on PATH
// - tmux present keeps the Go source-build path usable
// - tmux present keeps the prebuilt download path and checksum verification usable

//nolint:paralleltest // build.sh tests execute from the package root and may touch shared ./bin.
func TestBuildScriptMissingTmuxDoesNotInvokePackageManagers(t *testing.T) {
	pathDir := newToolPath(t, []string{toolCat, toolDirname, toolPwd})
	logPath := filepath.Join(t.TempDir(), "package-manager.log")
	for _, name := range []string{"brew", "apt-get", "dnf", "pacman"} {
		writeFakeTool(t, pathDir, name, fmt.Sprintf(`#!/bin/sh
echo "$0 $*" >> %q
exit 42
`, logPath))
	}

	cmd := exec.Command("sh", "scripts/build.sh")
	cmd.Env = append(os.Environ(), "PATH="+pathDir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err == nil {
		t.Fatal("build.sh succeeded without tmux; want failure")
	}
	if _, statErr := os.Stat(logPath); !os.IsNotExist(statErr) {
		t.Fatalf("package manager was invoked; log stat error = %v, log = %q", statErr, readFile(t, logPath))
	}
	got := stderr.String()
	for _, want := range []string{
		"tmux is required but was not found on PATH",
		"does not install system packages automatically",
		"Install tmux manually",
		"brew install tmux",
		"sudo apt-get install tmux",
		"sudo dnf install tmux",
		"sudo pacman -S tmux",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("stderr = %q, want it to contain %q", got, want)
		}
	}
}

//nolint:paralleltest // build.sh writes ./bin/toggle-popup, so these tests must stay serialized.
func TestBuildScriptSourceBuildPathStillWorksWhenTmuxExists(t *testing.T) {
	binPath := filepath.Join("bin", "toggle-popup")
	t.Cleanup(func() {
		_ = os.Remove(binPath)
	})

	pathDir := newToolPath(t, []string{toolCat, toolDirname, "mkdir", toolPwd})
	writeFakeTool(t, pathDir, "tmux", "#!/bin/sh\nexit 0\n")
	writeFakeTool(t, pathDir, "go", `#!/bin/sh
out=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-o" ]; then
    shift
    out="$1"
  fi
  shift
done
test -n "$out"
printf '%s\n' fake-go-binary > "$out"
`)

	cmd := exec.Command("sh", "scripts/build.sh")
	cmd.Env = append(os.Environ(), "PATH="+pathDir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build.sh failed: %v\n%s", err, output)
	}
	if got := readFile(t, binPath); got != "fake-go-binary\n" {
		t.Fatalf("%s = %q, want fake go output", binPath, got)
	}
}

//nolint:paralleltest // build.sh writes ./bin/toggle-popup, so these tests must stay serialized.
func TestBuildScriptDownloadPathStillVerifiesChecksumWhenTmuxExists(t *testing.T) {
	binPath := filepath.Join("bin", "toggle-popup")
	t.Cleanup(func() {
		_ = os.Remove(binPath)
	})

	pathDir := newToolPath(t, []string{"awk", toolCat, "chmod", toolDirname, "head", "mkdir", "mktemp", "mv", toolPwd, "rm", "sed"})
	writeFakeTool(t, pathDir, "tmux", "#!/bin/sh\nexit 0\n")
	writeFakeTool(t, pathDir, "uname", fmt.Sprintf(`#!/bin/sh
case "$1" in
  -s) printf '%%s\n' %q ;;
  -m) printf '%%s\n' %q ;;
  *) exit 64 ;;
esac
`, fakeOSName(), fakeArchName()))
	writeFakeTool(t, pathDir, "curl", `#!/bin/sh
out=""
url=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o)
      shift
      out="$1"
      ;;
    http*)
      url="$1"
      ;;
  esac
  shift
done
case "$url" in
  */checksums.txt)
    printf '%s  %s\n' expected-sha toggle-popup_`+fakeAssetSuffix()+` > "$out"
    ;;
  *)
    printf '%s\n' fake-downloaded-binary > "$out"
    ;;
esac
`)
	writeFakeTool(t, pathDir, "sha256sum", `#!/bin/sh
printf '%s  %s\n' expected-sha "$1"
`)

	cmd := exec.Command("sh", "scripts/build.sh")
	cmd.Env = append(os.Environ(), "PATH="+pathDir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build.sh failed: %v\n%s", err, output)
	}
	if got := readFile(t, binPath); got != "fake-downloaded-binary\n" {
		t.Fatalf("%s = %q, want fake downloaded output", binPath, got)
	}
}

func newToolPath(t *testing.T, realTools []string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range realTools {
		target, err := exec.LookPath(name)
		if err != nil {
			t.Fatalf("look up %s: %v", name, err)
		}
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Fatalf("symlink %s: %v", name, err)
		}
	}
	return dir
}

func writeFakeTool(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, name)
	//nolint:gosec // Fake tools must be executable by the subprocess under test.
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake %s: %v", name, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	//nolint:gosec // Test helper only reads paths created by this test package.
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func fakeOSName() string {
	if runtime.GOOS == goosDarwin {
		return "Darwin"
	}
	return "Linux"
}

func fakeArchName() string {
	if runtime.GOARCH == goarchARM {
		return goarchARM
	}
	return "x86_64"
}

func fakeAssetSuffix() string {
	osPart := "linux"
	if runtime.GOOS == goosDarwin {
		osPart = goosDarwin
	}
	archPart := "amd64"
	if runtime.GOARCH == goarchARM {
		archPart = goarchARM
	}
	return osPart + "_" + archPart
}
