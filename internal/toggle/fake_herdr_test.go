package toggle

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

// fakeHerdrScript stands in for the real herdr CLI, the same way tests/toggle.bats stubs it:
// every invocation is appended to $STUB_HERDR_LOG as one line, and env var knobs
// (STUB_HERDR_*_EXIT and friends) control exit codes and response bodies per subcommand.
const fakeHerdrScript = `#!/usr/bin/env bash
set -euo pipefail
: "${STUB_HERDR_LOG:?STUB_HERDR_LOG must be set}"
printf '%s\n' "$*" >> "$STUB_HERDR_LOG"

case "$1 $2" in
  "plugin pane")
    case "$3" in
      open)
        exit_code="${STUB_HERDR_OPEN_EXIT:-0}"
        if [ "$exit_code" -ne 0 ]; then
          printf 'stub open failure\n' >&2
          exit "$exit_code"
        fi
        pane_id="${STUB_HERDR_OPEN_PANE_ID:-new-pane-1}"
        printf '{"result":{"plugin_pane":{"pane":{"pane_id":"%s"}}}}\n' "$pane_id"
        ;;
      close)
        exit_code="${STUB_HERDR_CLOSE_EXIT:-0}"
        if [ "$exit_code" -ne 0 ]; then
          printf 'stub close failure\n' >&2
          exit "$exit_code"
        fi
        printf '{"result":{"type":"plugin_pane_closed"}}\n'
        ;;
      focus)
        exit_code="${STUB_HERDR_FOCUS_EXIT:-0}"
        if [ "$exit_code" -ne 0 ]; then
          printf 'stub focus failure\n' >&2
          exit "$exit_code"
        fi
        printf '{"result":{"type":"plugin_pane_focused"}}\n'
        ;;
    esac
    ;;
  "pane get")
    exit_code="${STUB_HERDR_GET_EXIT:-0}"
    if [ "$exit_code" -ne 0 ]; then
      printf 'stub get failure\n' >&2
      exit "$exit_code"
    fi
    printf '{"result":{"pane":{"pane_id":"%s"}}}\n' "$3"
    ;;
  "pane layout")
    exit_code="${STUB_HERDR_LAYOUT_EXIT:-0}"
    if [ "$exit_code" -ne 0 ]; then
      printf 'stub layout failure\n' >&2
      exit "$exit_code"
    fi
    if [ "${STUB_HERDR_LAYOUT_SOLO:-0}" -eq 1 ]; then
      printf '{"result":{"layout":{"panes":[{"pane_id":"%s"}]}}}\n' "$4"
    else
      printf '{"result":{"layout":{"panes":[{"pane_id":"%s"},{"pane_id":"pane-sibling"}]}}}\n' "$4"
    fi
    ;;
  "pane zoom")
    exit_code="${STUB_HERDR_ZOOM_EXIT:-0}"
    if [ "$exit_code" -ne 0 ]; then
      printf 'stub zoom failure\n' >&2
      exit "$exit_code"
    fi
    printf '{"result":{"zoom":{"changed":true}}}\n'
    ;;
  "pane resize")
    exit_code="${STUB_HERDR_RESIZE_EXIT:-0}"
    if [ "$exit_code" -ne 0 ]; then
      printf 'stub resize failure\n' >&2
      exit "$exit_code"
    fi
    printf '{"result":{"type":"pane_resized"}}\n'
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
