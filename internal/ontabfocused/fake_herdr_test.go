package ontabfocused

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const (
	fakeHerdrExecPerm = 0o700
	fakeHerdrLogPerm  = 0o600
	fakeHerdrLogEnv   = "STUB_HERDR_LOG"
	binPathEnvVar     = "HERDR_BIN_PATH"
)

// fakeHerdrScript stands in for the real herdr CLI: every invocation is appended to
// $STUB_HERDR_LOG as one line. STUB_HERDR_CLOSE_FAIL_PANE_ID, if set, makes `plugin pane close
// <that pane id>` fail while every other close call still succeeds, so tests can force a single
// entry's close to fail without affecting the others processed in the same run.
const fakeHerdrScript = `#!/usr/bin/env bash
set -euo pipefail
: "${STUB_HERDR_LOG:?STUB_HERDR_LOG must be set}"
printf '%s\n' "$*" >> "$STUB_HERDR_LOG"

case "$1 $2" in
  "plugin pane")
    case "$3" in
      close)
        pane_id="$4"
        if [ -n "${STUB_HERDR_CLOSE_FAIL_PANE_ID:-}" ] && [ "$pane_id" = "$STUB_HERDR_CLOSE_FAIL_PANE_ID" ]; then
          printf 'stub close failure\n' >&2
          exit 1
        fi
        printf '{"result":{"type":"plugin_pane_closed"}}\n'
        ;;
      *)
        printf 'stub herdr: unhandled args: %s\n' "$*" >&2
        exit 99
        ;;
    esac
    ;;
  *)
    printf 'stub herdr: unhandled args: %s\n' "$*" >&2
    exit 99
    ;;
esac
`

// newFakeHerdr writes the fake herdr script to a temp dir, points HERDR_BIN_PATH and
// STUB_HERDR_LOG at it, and returns the log file path. Callers set further STUB_HERDR_* env
// vars via t.Setenv before invoking Run.
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
