package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Test list for scripts/verify-reproducible-build.sh:
// - no version argument prints usage and exits non-zero
// - an empty version argument is rejected
// - missing go fails before any build starts
// - every release target is built twice, from two different source paths
// - each of the two builds has an empty GOCACHE of its own
// - every build passes -trimpath, CGO_ENABLED=0, and the expected version
// - matching digests print one digest per release target
// - a digest mismatch fails, naming the target and both digests
// - a failed go build fails, naming the target
// - a binary reporting another version fails, naming both versions
// - the real repository builds reproducibly for all four targets and embeds the manifest version

const (
	reproducibleScriptName   = "scripts/verify-reproducible-build.sh"
	reproducibleVersion      = "9.9.9"
	reproducibleOtherVersion = "0.0.1"
	reproducibleHostOS       = "linux"
	reproducibleHostArch     = "amd64"
	reproducibleUsage        = "usage: sh scripts/verify-reproducible-build.sh <version>"
)

// reproducibleBuildCall is one fake `go build` invocation recorded by fakeReproducibleGo.
type reproducibleBuildCall struct {
	pwd     string
	gocache string
	goos    string
	goarch  string
	cgo     string
	args    string
}

//nolint:paralleltest // The script spawns shell processes; running them in parallel starves other tests' command timeouts.
func TestReproducibleBuildScriptWithoutVersionPrintsUsage(t *testing.T) {
	root := stageReproducibleScript(t)

	_, stderr, err := runReproducibleScript(t, root, fakeReproducibleEnv(t, t.TempDir()))

	if err == nil {
		t.Fatal("verify-reproducible-build.sh succeeded without a version; want failure")
	}
	if !strings.Contains(stderr, reproducibleUsage) {
		t.Fatalf("stderr = %q, want it to contain %q", stderr, reproducibleUsage)
	}
}

//nolint:paralleltest // The script spawns shell processes; running them in parallel starves other tests' command timeouts.
func TestReproducibleBuildScriptRejectsEmptyVersion(t *testing.T) {
	root := stageReproducibleScript(t)

	_, stderr, err := runReproducibleScript(t, root, fakeReproducibleEnv(t, t.TempDir()), "")

	if err == nil {
		t.Fatal("verify-reproducible-build.sh accepted an empty version; want failure")
	}
	if !strings.Contains(stderr, reproducibleUsage) {
		t.Fatalf("stderr = %q, want it to contain %q", stderr, reproducibleUsage)
	}
}

//nolint:paralleltest // The script spawns shell processes; running them in parallel starves other tests' command timeouts.
func TestReproducibleBuildScriptWithoutGoFails(t *testing.T) {
	root := stageReproducibleScript(t)
	env := []string{
		"PATH=" + newToolPath(t, reproduciblePOSIXTools()),
		"REPRO_GO_LOG=" + filepath.Join(t.TempDir(), "go.log"),
		"TMPDIR=" + t.TempDir(),
	}

	_, stderr, err := runReproducibleScript(t, root, env, reproducibleVersion)

	if err == nil {
		t.Fatal("verify-reproducible-build.sh succeeded without go; want failure")
	}
	if !strings.Contains(stderr, "go is required on PATH") {
		t.Fatalf("stderr = %q, want it to name the missing go tool", stderr)
	}
}

//nolint:paralleltest // The script spawns shell processes; running them in parallel starves other tests' command timeouts.
func TestReproducibleBuildScriptBuildsEachTargetTwiceFromDifferentPaths(t *testing.T) {
	root := stageReproducibleScript(t)
	logPath := filepath.Join(t.TempDir(), "go.log")

	_, stderr, err := runReproducibleScript(t, root, fakeReproducibleEnv(t, logPath), reproducibleVersion)
	if err != nil {
		t.Fatalf("verify-reproducible-build.sh failed: %v\n%s", err, stderr)
	}

	calls := parseReproducibleBuildCalls(t, readFile(t, logPath))

	assertEveryReleaseTargetBuiltTwice(t, calls)
	assertFixedBuildInputs(t, calls)
	assertIndependentBuilds(t, calls)
}

// assertEveryReleaseTargetBuiltTwice checks that each release target has two builds in the log.
func assertEveryReleaseTargetBuiltTwice(t *testing.T, calls []reproducibleBuildCall) {
	t.Helper()

	counts := map[string]int{}
	for _, call := range calls {
		counts[call.goos+"/"+call.goarch]++
	}
	wantTargets := []string{"darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64"}
	for _, target := range wantTargets {
		if counts[target] != 2 {
			t.Errorf("%s built %d times, want 2 independent builds", target, counts[target])
		}
	}
	if total := len(calls); total != len(wantTargets)*2 {
		t.Errorf("go build invocations = %d, want %d", total, len(wantTargets)*2)
	}
}

// assertFixedBuildInputs checks the flags every recorded build ran with.
func assertFixedBuildInputs(t *testing.T, calls []reproducibleBuildCall) {
	t.Helper()

	for _, call := range calls {
		if call.cgo != "0" {
			t.Errorf("CGO_ENABLED = %q, want 0", call.cgo)
		}
		if !strings.Contains(call.args, "-trimpath") {
			t.Errorf("build args = %q, want -trimpath", call.args)
		}
		if !strings.Contains(call.args, "-X main.version="+reproducibleVersion) {
			t.Errorf("build args = %q, want it to embed version %s", call.args, reproducibleVersion)
		}
	}
}

// assertIndependentBuilds checks the log holds two source paths with a separate GOCACHE each.
func assertIndependentBuilds(t *testing.T, calls []reproducibleBuildCall) {
	t.Helper()

	sourcePaths := map[string]bool{}
	caches := map[string]bool{}
	for _, call := range calls {
		sourcePaths[call.pwd] = true
		caches[call.gocache] = true
	}
	if len(sourcePaths) != 2 {
		t.Errorf("source paths = %v, want two different builds", sourcePaths)
	}
	if len(caches) != 2 {
		t.Errorf("GOCACHE values = %v, want one per independent build", caches)
	}
	for cache := range caches {
		if cache == "" {
			t.Error("a build ran with an empty GOCACHE")
		}
	}
}

//nolint:paralleltest // The script spawns shell processes; running them in parallel starves other tests' command timeouts.
func TestReproducibleBuildScriptPrintsOneDigestPerReleaseTarget(t *testing.T) {
	root := stageReproducibleScript(t)
	logPath := filepath.Join(t.TempDir(), "go.log")

	stdout, stderr, err := runReproducibleScript(t, root, fakeReproducibleEnv(t, logPath), reproducibleVersion)
	if err != nil {
		t.Fatalf("verify-reproducible-build.sh failed: %v\n%s", err, stderr)
	}

	digestLine := regexp.MustCompile(`(?m)^[0-9a-f]{64}  toggle-popup_(darwin|linux)_(amd64|arm64)$`)
	if got := len(digestLine.FindAllString(stdout, -1)); got != 4 {
		t.Errorf("digest lines = %d, want 4\nstdout:\n%s", got, stdout)
	}
	for _, asset := range []string{
		"toggle-popup_darwin_amd64",
		"toggle-popup_darwin_arm64",
		"toggle-popup_linux_amd64",
		"toggle-popup_linux_arm64",
	} {
		if !strings.Contains(stdout, asset) {
			t.Errorf("stdout does not name %s\nstdout:\n%s", asset, stdout)
		}
	}
	if !strings.Contains(stdout, "reports version "+reproducibleVersion) {
		t.Errorf("stdout = %q, want it to confirm the embedded version", stdout)
	}
}

//nolint:paralleltest // The script spawns shell processes; running them in parallel starves other tests' command timeouts.
func TestReproducibleBuildScriptFailsOnDigestMismatch(t *testing.T) {
	root := stageReproducibleScript(t)
	logPath := filepath.Join(t.TempDir(), "go.log")
	env := append(fakeReproducibleEnv(t, logPath), "REPRO_GO_PATH_COMMENT=1")

	_, stderr, err := runReproducibleScript(t, root, env, reproducibleVersion)

	if err == nil {
		t.Fatal("verify-reproducible-build.sh passed with differing digests; want failure")
	}
	for _, want := range []string{
		"digest mismatch for toggle-popup_darwin_amd64",
		"first build:",
		"second build:",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, want it to contain %q", stderr, want)
		}
	}
}

//nolint:paralleltest // The script spawns shell processes; running them in parallel starves other tests' command timeouts.
func TestReproducibleBuildScriptFailsWhenABuildFails(t *testing.T) {
	root := stageReproducibleScript(t)
	logPath := filepath.Join(t.TempDir(), "go.log")
	env := append(fakeReproducibleEnv(t, logPath), "REPRO_GO_FAIL_TARGET=linux/arm64")

	_, stderr, err := runReproducibleScript(t, root, env, reproducibleVersion)

	if err == nil {
		t.Fatal("verify-reproducible-build.sh succeeded while a build failed; want failure")
	}
	if !strings.Contains(stderr, "go build failed for linux/arm64") {
		t.Fatalf("stderr = %q, want it to name the failed target", stderr)
	}
}

//nolint:paralleltest // The script spawns shell processes; running them in parallel starves other tests' command timeouts.
func TestReproducibleBuildScriptFailsWhenEmbeddedVersionDiffers(t *testing.T) {
	root := stageReproducibleScript(t)
	logPath := filepath.Join(t.TempDir(), "go.log")
	env := append(fakeReproducibleEnv(t, logPath), "REPRO_GO_VERSION="+reproducibleOtherVersion)

	_, stderr, err := runReproducibleScript(t, root, env, reproducibleVersion)

	if err == nil {
		t.Fatal("verify-reproducible-build.sh passed with the wrong embedded version; want failure")
	}
	for _, want := range []string{
		"reports version " + reproducibleOtherVersion,
		"expected " + reproducibleVersion,
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, want it to contain %q", stderr, want)
		}
	}
}

// The end-to-end run is the check CI executes: it fails `go test ./...` when the real release
// targets stop building reproducibly.
//
//nolint:paralleltest // Two independent cold builds compete for CPU with every other package's tests.
func TestReproducibleBuildScriptMatchesDigestsForRealReleaseTargets(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is required to verify release builds")
	}

	version := manifestVersion(t)
	stdout, stderr, err := runReproducibleScript(t, ".", os.Environ(), version)
	if err != nil {
		t.Fatalf("verify-reproducible-build.sh failed on this repository: %v\n%s", err, stderr)
	}

	digestLine := regexp.MustCompile(`(?m)^[0-9a-f]{64}  toggle-popup_(darwin|linux)_(amd64|arm64)$`)
	if got := len(digestLine.FindAllString(stdout, -1)); got != 4 {
		t.Errorf("digest lines = %d, want 4\nstdout:\n%s", got, stdout)
	}
	if !strings.Contains(stdout, "reports version "+version) {
		t.Errorf("stdout = %q, want it to confirm the manifest version", stdout)
	}
}

// stageReproducibleScript copies the verifier into a fixture tree, so the script builds the
// fixture's contents instead of this repository.
func stageReproducibleScript(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	scriptDir := filepath.Join(root, "scripts")
	if err := os.MkdirAll(scriptDir, 0o750); err != nil {
		t.Fatalf("create fixture scripts dir: %v", err)
	}
	script, err := os.ReadFile(reproducibleScriptName)
	if err != nil {
		t.Fatalf("read %s: %v", reproducibleScriptName, err)
	}
	//nolint:gosec // Fixture script must be executable by the subprocess under test.
	if err := os.WriteFile(filepath.Join(scriptDir, "verify-reproducible-build.sh"), script, 0o755); err != nil {
		t.Fatalf("write fixture verifier: %v", err)
	}
	return root
}

// fakeReproducibleEnv builds a PATH holding the POSIX tools the verifier uses plus a fake go,
// and points the fake go's log at goLog.
func fakeReproducibleEnv(t *testing.T, goLog string) []string {
	t.Helper()

	pathDir := newToolPath(t, reproduciblePOSIXTools())
	writeFakeTool(t, pathDir, "go", fakeReproducibleGo())
	return []string{
		"PATH=" + pathDir,
		"REPRO_GO_LOG=" + goLog,
		"TMPDIR=" + t.TempDir(),
	}
}

// reproduciblePOSIXTools lists the tools the verifier needs, including whichever SHA-256 tool
// this platform has.
func reproduciblePOSIXTools() []string {
	tools := []string{"awk", "chmod", "cp", "dirname", "mkdir", "mktemp", "rm"}
	for _, name := range []string{"sha256sum", "shasum"} {
		if _, err := exec.LookPath(name); err == nil {
			tools = append(tools, name)
		}
	}
	return tools
}

// fakeReproducibleGo writes a stand-in binary that prints the version from ldflags, so the
// verifier's own version check runs. The REPRO_GO_* variables let a test change one input.
func fakeReproducibleGo() string {
	return `#!/bin/sh
if [ "$1" = "env" ]; then
  case "$2" in
    GOOS) printf '%s\n' ` + reproducibleHostOS + ` ;;
    GOARCH) printf '%s\n' ` + reproducibleHostArch + ` ;;
    *) exit 64 ;;
  esac
  exit 0
fi
if [ -n "$REPRO_GO_FAIL_TARGET" ] && [ "$GOOS/$GOARCH" = "$REPRO_GO_FAIL_TARGET" ]; then
  echo "fake go: build failed for $GOOS/$GOARCH" >&2
  exit 1
fi
printf 'pwd=%s gocache=%s goos=%s goarch=%s cgo=%s args=%s\n' "$PWD" "$GOCACHE" "$GOOS" "$GOARCH" "$CGO_ENABLED" "$*" >> "$REPRO_GO_LOG"
out=""
ldflags=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o)
      shift
      out="$1"
      ;;
    -ldflags)
      shift
      ldflags="$1"
      ;;
  esac
  shift
done
test -n "$out"
version="${ldflags##*-X main.version=}"
if [ -n "$REPRO_GO_VERSION" ]; then
  version="$REPRO_GO_VERSION"
fi
{
  printf '#!/bin/sh\n'
  printf 'printf "%%s\n" "%s"\n' "$version"
  if [ -n "$REPRO_GO_PATH_COMMENT" ]; then
    printf '# built in %s\n' "$PWD"
  fi
} > "$out"
chmod +x "$out"
`
}

// runReproducibleScript runs the verifier from root and returns its streams.
func runReproducibleScript(t *testing.T, root string, env []string, args ...string) (stdout, stderr string, err error) {
	t.Helper()

	scriptArgs := append([]string{filepath.Join(root, reproducibleScriptName)}, args...)
	//nolint:gosec // The script path is built from a fixture directory or the package root.
	cmd := exec.Command("sh", scriptArgs...)
	cmd.Env = env
	cmd.Dir = root
	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err = cmd.Run()
	return stdoutBuf.String(), stderrBuf.String(), err
}

// parseReproducibleBuildCalls reads the log fakeReproducibleGo appends to.
func parseReproducibleBuildCalls(t *testing.T, log string) []reproducibleBuildCall {
	t.Helper()

	var calls []reproducibleBuildCall
	for line := range strings.SplitSeq(strings.TrimSpace(log), "\n") {
		if line == "" {
			continue
		}
		head, args, ok := strings.Cut(line, " args=")
		if !ok {
			t.Fatalf("unexpected fake go log line: %q", line)
		}
		call := reproducibleBuildCall{pwd: "", gocache: "", goos: "", goarch: "", cgo: "", args: args}
		for field := range strings.FieldsSeq(head) {
			key, value, ok := strings.Cut(field, "=")
			if !ok {
				t.Fatalf("unexpected fake go log field: %q", field)
			}
			setReproducibleBuildField(&call, key, value)
		}
		calls = append(calls, call)
	}
	return calls
}

// setReproducibleBuildField copies one key=value pair from the fake go log onto call.
func setReproducibleBuildField(call *reproducibleBuildCall, key, value string) {
	switch key {
	case "pwd":
		call.pwd = value
	case "gocache":
		call.gocache = value
	case "goos":
		call.goos = value
	case "goarch":
		call.goarch = value
	case "cgo":
		call.cgo = value
	}
}

// manifestVersion reads the version the release workflow embeds from herdr-plugin.toml.
func manifestVersion(t *testing.T) string {
	t.Helper()

	manifest := readFile(t, "herdr-plugin.toml")
	matches := regexp.MustCompile(`(?m)^version\s*=\s*"([^"]+)"`).FindStringSubmatch(manifest)
	if matches == nil {
		t.Fatal("herdr-plugin.toml has no version")
	}
	return matches[1]
}
