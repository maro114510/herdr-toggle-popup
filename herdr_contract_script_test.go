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

// Test list for scripts/herdr-contract.sh:
//
// - minimum: resolves the tag from min_herdr_version, verifies the version, and records tag,
//   revision, and version before running the contract test
// - stable: resolves the latest stable release and runs the contract against that version
// - preview: resolves the latest prerelease and accepts a version carrying its revision
// - stable: a binary that does not report the resolved version fails before the contract test
// - preview: a binary that does not carry the resolved revision fails before the contract test
// - a failed asset download fails before the contract test
// - an unknown track is rejected before any GitHub API call

const (
	stubRevision      = "0123456789abcdef0123456789abcdef01234567"
	stubOtherRevision = "fedcba9876543210fedcba9876543210fedcba98"
)

// contractStub holds the fake release API, download, and Go toolchain one contract run sees.
type contractStub struct {
	env   []string
	goLog string
	ghLog string
}

// newContractStub writes fakes for gh, curl, go, and tmux onto a private PATH. The fakes print the
// values the test supplies, so the test never reaches GitHub or the network.
func newContractStub(t *testing.T, herdrVersion, stableTag, previewTag, revision string) *contractStub {
	t.Helper()

	pathDir := newToolPath(t, []string{"chmod", "cut", "dirname", "head", "mkdir", "mktemp", "rm", "sed", "uname"})
	goLog := filepath.Join(t.TempDir(), "go.log")
	ghLog := filepath.Join(t.TempDir(), "gh.log")

	writeFakeTool(t, pathDir, "gh", `#!/bin/sh
printf '%s\n' "$*" >> "$STUB_GH_LOG"
case "$*" in
  *releases/latest*) printf '%s\n' "$STUB_STABLE_TAG" ;;
  *per_page=30*) printf '%s\n' "$STUB_PREVIEW_TAG" ;;
  *commits/*) printf '%s\n' "$STUB_REVISION" ;;
  *) exit 1 ;;
esac
`)
	writeFakeTool(t, pathDir, "curl", `#!/bin/sh
out=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o) shift; out=$1 ;;
  esac
  shift
done
[ -n "$out" ] || exit 1
if [ "${STUB_CURL_FAIL:-0}" = "1" ]; then exit 22; fi
printf '#!/bin/sh\nprintf "%%s\\n" "%s"\n' "$STUB_HERDR_VERSION" > "$out"
`)
	writeFakeTool(t, pathDir, "go", `#!/bin/sh
{
  printf 'args=%s\n' "$*"
  printf 'bin=%s\n' "${HERDR_CONTRACT_BIN:-}"
  printf 'track=%s\n' "${HERDR_CONTRACT_TRACK:-}"
  printf 'require=%s\n' "${HERDR_CONTRACT_REQUIRE:-}"
} > "$STUB_GO_LOG"
exit "${STUB_GO_EXIT:-0}"
`)
	writeFakeTool(t, pathDir, "tmux", `#!/bin/sh
printf 'tmux %s\n' "${STUB_TMUX_VERSION:-3.8}"
`)

	env := append(os.Environ(),
		"PATH="+pathDir,
		"STUB_GO_LOG="+goLog,
		"STUB_GH_LOG="+ghLog,
		"STUB_HERDR_VERSION="+herdrVersion,
		"STUB_STABLE_TAG="+stableTag,
		"STUB_PREVIEW_TAG="+previewTag,
		"STUB_REVISION="+revision,
	)
	return &contractStub{env: env, goLog: goLog, ghLog: ghLog}
}

// run executes the script for track from the repository root.
func (s *contractStub) run(t *testing.T, track string) (stdout, stderr string, code int) {
	t.Helper()

	//nolint:gosec // The test runs the repository's own script with fixed arguments.
	cmd := exec.Command("sh", "scripts/herdr-contract.sh", track)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	cmd.Env = s.env
	err := cmd.Run()
	if err == nil {
		return out.String(), errBuf.String(), 0
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("run scripts/herdr-contract.sh %s: %v", track, err)
	}
	return out.String(), errBuf.String(), exitErr.ExitCode()
}

func (s *contractStub) wantNoContractRun(t *testing.T) {
	t.Helper()

	if _, err := os.Stat(s.goLog); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the contract test ran when acquisition should have failed: %s", readFile(t, s.goLog))
	}
}

func TestHerdrContractScriptMinimumResolvesManifestFloor(t *testing.T) {
	t.Parallel()

	stub := newContractStub(t, "herdr 0.9.3", "v0.9.5", "preview-2026-10-14-"+stubRevision[:12], stubRevision)
	stdout, stderr, code := stub.run(t, "minimum")
	if code != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	for _, want := range []string{
		"track=minimum",
		"version=0.9.3",
		"tag=v0.9.3",
		"revision=" + stubRevision,
		"tmux=3.8",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want it to contain %q", stdout, want)
		}
	}
	goLog := readFile(t, stub.goLog)
	for _, want := range []string{
		"-run ^TestHerdrContract$",
		"track=minimum",
		"require=1",
		"/bin/herdr",
	} {
		if !strings.Contains(goLog, want) {
			t.Fatalf("go log = %q, want it to contain %q", goLog, want)
		}
	}
}

func TestHerdrContractScriptStableResolvesLatestRelease(t *testing.T) {
	t.Parallel()

	stub := newContractStub(t, "herdr 0.9.5", "v0.9.5", "preview-2026-10-14-"+stubRevision[:12], stubRevision)
	stdout, stderr, code := stub.run(t, "stable")
	if code != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	for _, want := range []string{
		"track=stable",
		"version=0.9.5",
		"tag=v0.9.5",
		"revision=" + stubRevision,
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want it to contain %q", stdout, want)
		}
	}
	if !strings.Contains(readFile(t, stub.ghLog), "releases/latest") {
		t.Fatalf("gh log = %q, want the latest release lookup", readFile(t, stub.ghLog))
	}
}

func TestHerdrContractScriptPreviewAcceptsRevisionInVersion(t *testing.T) {
	t.Parallel()

	previewTag := "preview-2026-10-14-" + stubRevision[:12]
	stub := newContractStub(t, "herdr 0.9.4-preview.2026-10-14-"+stubRevision[:12], "v0.9.5", previewTag, stubRevision)
	stdout, stderr, code := stub.run(t, "preview")
	if code != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	for _, want := range []string{
		"track=preview",
		"version=0.9.4-preview.2026-10-14-" + stubRevision[:12],
		"tag=" + previewTag,
		"revision=" + stubRevision,
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want it to contain %q", stdout, want)
		}
	}
}

func TestHerdrContractScriptRejectsWrongStableVersion(t *testing.T) {
	t.Parallel()

	stub := newContractStub(t, "herdr 0.9.3", "v0.9.5", "preview-2026-10-14-"+stubRevision[:12], stubRevision)
	_, stderr, code := stub.run(t, "stable")
	if code == 0 {
		t.Fatalf("exit %d, want a failure for a mismatched stable binary", code)
	}
	if want := "expected herdr 0.9.5; got herdr 0.9.3"; !strings.Contains(stderr, want) {
		t.Fatalf("stderr = %q, want it to contain %q", stderr, want)
	}
	stub.wantNoContractRun(t)
}

func TestHerdrContractScriptRejectsPreviewWithoutRevision(t *testing.T) {
	t.Parallel()

	stub := newContractStub(t, "herdr 0.9.4-preview.2026-10-14-"+stubOtherRevision[:12], "v0.9.5", "preview-2026-10-14-"+stubRevision[:12], stubRevision)
	_, stderr, code := stub.run(t, "preview")
	if code == 0 {
		t.Fatalf("exit %d, want a failure for a preview binary from another revision", code)
	}
	if want := stubRevision[:12]; !strings.Contains(stderr, want) {
		t.Fatalf("stderr = %q, want it to name the expected revision %q", stderr, want)
	}
	stub.wantNoContractRun(t)
}

func TestHerdrContractScriptFailsWhenDownloadFails(t *testing.T) {
	t.Parallel()

	stub := newContractStub(t, "herdr 0.9.5", "v0.9.5", "preview-2026-10-14-"+stubRevision[:12], stubRevision)
	stub.env = append(stub.env, "STUB_CURL_FAIL=1")
	_, stderr, code := stub.run(t, "stable")
	if code == 0 {
		t.Fatalf("exit %d, want a failure when the asset download fails", code)
	}
	if want := "failed to download"; !strings.Contains(stderr, want) {
		t.Fatalf("stderr = %q, want it to contain %q", stderr, want)
	}
	stub.wantNoContractRun(t)
}

func TestHerdrContractScriptRejectsUnknownTrackBeforeLookup(t *testing.T) {
	t.Parallel()

	stub := newContractStub(t, "herdr 0.9.5", "v0.9.5", "preview-2026-10-14-"+stubRevision[:12], stubRevision)
	_, stderr, code := stub.run(t, "nightly")
	if code != 2 {
		t.Fatalf("exit %d, want usage exit 2", code)
	}
	if want := "unknown track"; !strings.Contains(stderr, want) {
		t.Fatalf("stderr = %q, want it to contain %q", stderr, want)
	}
	if _, err := os.Stat(stub.ghLog); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("gh ran for an unknown track: %s", readFile(t, stub.ghLog))
	}
}
