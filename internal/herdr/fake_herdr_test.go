package herdr

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const (
	fakeHerdrExecPerm = 0o700
	fakeHerdrLogPerm  = 0o600
	fakeHerdrLogEnv   = "FAKE_HERDR_LOG"
)

// fakeHerdrScript stands in for the real herdr CLI in client tests, the same
// way tests/toggle.bats stubs it: every invocation is appended to
// $FAKE_HERDR_LOG as one line, and env var knobs (FAKE_HERDR_*_EXIT and
// friends) control exit codes and response bodies.
const fakeHerdrScript = `#!/usr/bin/env bash
set -euo pipefail
: "${FAKE_HERDR_LOG:?FAKE_HERDR_LOG must be set}"
printf '%s\n' "$*" >> "$FAKE_HERDR_LOG"

assert_args() {
  expected="$1"
  shift
  if [ "$*" != "$expected" ]; then
    printf 'fake herdr: unexpected args: got %s, want %s\n' "$*" "$expected" >&2
    exit 98
  fi
}

case "$1 $2 $3" in
  "plugin pane open")
    assert_args "plugin pane open --plugin maro114510.toggle-popup --entrypoint shell --placement popup --width ${FAKE_HERDR_EXPECT_WIDTH:-100%} --height ${FAKE_HERDR_EXPECT_HEIGHT:-100%} --cwd /focused/cwd --focus" "$@"
    exit_code="${FAKE_HERDR_OPEN_EXIT:-0}"
    if [ "$exit_code" -ne 0 ]; then
      printf 'stub open failure\n' >&2
      exit "$exit_code"
    fi
    if [ -n "${FAKE_HERDR_OPEN_DELAY_SECONDS:-}" ]; then
      sleep "$FAKE_HERDR_OPEN_DELAY_SECONDS"
    fi
    if [ "${FAKE_HERDR_OPEN_MALFORMED:-0}" -eq 1 ]; then
      printf 'not-json\n'
      exit 0
    fi
    printf '{"result":{"type":"ok"}}\n'
    ;;
  *)
    printf 'fake herdr: unhandled args: %s\n' "$*" >&2
    exit 99
    ;;
esac
`

// newFakeHerdr writes the fake herdr script to a temp dir, points
// HERDR_BIN_PATH and FAKE_HERDR_LOG at it, and returns the log file path.
// Callers set further FAKE_HERDR_* env vars via t.Setenv before invoking the
// client under test.
func newFakeHerdr(t *testing.T) string {
	t.Helper()

	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found, skipping fake-herdr test")
	}

	dir := t.TempDir()
	bin := filepath.Join(dir, "herdr")
	if err := os.WriteFile(bin, []byte(fakeHerdrScript), fakeHerdrExecPerm); err != nil {
		t.Fatal(err)
	}

	logPath := filepath.Join(dir, "calls.log")
	if err := os.WriteFile(logPath, nil, fakeHerdrLogPerm); err != nil {
		t.Fatal(err)
	}

	t.Setenv(binPathEnvVar, bin)
	t.Setenv(fakeHerdrLogEnv, logPath)

	return logPath
}

// readLog returns the fake herdr call log's contents.
func readLog(t *testing.T, logPath string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Clean(logPath))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
