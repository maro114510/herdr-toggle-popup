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
// friends) control exit codes and response bodies per subcommand.
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

case "$1 $2" in
  "plugin pane")
    case "$3" in
      open)
        assert_args 'plugin pane open --plugin maro114510.toggle-popup --entrypoint shell --placement overlay --cwd /focused/cwd --focus' "$@"
        exit_code="${FAKE_HERDR_OPEN_EXIT:-0}"
        if [ "$exit_code" -ne 0 ]; then
          printf 'stub open failure\n' >&2
          exit "$exit_code"
        fi
        if [ "${FAKE_HERDR_OPEN_MALFORMED:-0}" -eq 1 ]; then
          printf 'not-json\n'
          exit 0
        fi
        if [ "${FAKE_HERDR_OPEN_MISSING_PANE_ID:-0}" -eq 1 ]; then
          printf '{"result":{"plugin_pane":{"pane":{}}}}\n'
          exit 0
        fi
        if [ -n "${FAKE_HERDR_OPEN_DELAY_SECONDS:-}" ]; then
          sleep "$FAKE_HERDR_OPEN_DELAY_SECONDS"
        fi
        pane_id="${FAKE_HERDR_OPEN_PANE_ID:-new-pane-1}"
        if [ "${FAKE_HERDR_OPEN_OMIT_TAB_ID:-0}" -eq 1 ]; then
          printf '{"result":{"plugin_pane":{"pane":{"pane_id":"%s"}}}}\n' "$pane_id"
        else
          tab_id="${FAKE_HERDR_OPEN_TAB_ID:-tab-1}"
          printf '{"result":{"plugin_pane":{"pane":{"pane_id":"%s","tab_id":"%s"}}}}\n' "$pane_id" "$tab_id"
        fi
        exit 0
        ;;
      close)
        assert_args 'plugin pane close pane-1' "$@"
        exit_code="${FAKE_HERDR_CLOSE_EXIT:-0}"
        if [ "$exit_code" -ne 0 ]; then
          printf 'stub close failure\n' >&2
          exit "$exit_code"
        fi
        printf '{"result":{"type":"plugin_pane_closed"}}\n'
        ;;
      focus)
        assert_args 'plugin pane focus pane-1' "$@"
        exit_code="${FAKE_HERDR_FOCUS_EXIT:-0}"
        if [ "$exit_code" -ne 0 ]; then
          printf 'stub focus failure\n' >&2
          exit "$exit_code"
        fi
        printf '{"result":{"type":"plugin_pane_focused"}}\n'
        ;;
    esac
    ;;
  "pane get")
    assert_args 'pane get pane-1' "$@"
    exit_code="${FAKE_HERDR_GET_EXIT:-0}"
    if [ "$exit_code" -ne 0 ]; then
      printf 'stub get failure\n' >&2
      exit "$exit_code"
    fi
    printf '{"result":{"pane":{"pane_id":"%s"}}}\n' "$3"
    ;;
  "pane layout")
    assert_args 'pane layout --pane pane-1' "$@"
    exit_code="${FAKE_HERDR_LAYOUT_EXIT:-0}"
    if [ "$exit_code" -ne 0 ]; then
      printf 'stub layout failure\n' >&2
      exit "$exit_code"
    fi
    if [ "${FAKE_HERDR_LAYOUT_MALFORMED:-0}" -eq 1 ]; then
      printf 'not-json\n'
      exit 0
    fi
    if [ "${FAKE_HERDR_LAYOUT_SOLO:-0}" -eq 1 ]; then
      printf '{"result":{"layout":{"panes":[{"pane_id":"%s"}]}}}\n' "$4"
    else
      printf '{"result":{"layout":{"panes":[{"pane_id":"%s"},{"pane_id":"pane-sibling"}]}}}\n' "$4"
    fi
    ;;
  "pane zoom")
    assert_args 'pane zoom pane-1 --on' "$@"
    exit_code="${FAKE_HERDR_ZOOM_EXIT:-0}"
    if [ "$exit_code" -ne 0 ]; then
      printf 'stub zoom failure\n' >&2
      exit "$exit_code"
    fi
    printf '{"result":{"zoom":{"changed":true}}}\n'
    ;;
  "pane resize")
    assert_args 'pane resize --direction right --amount 0.5 --pane pane-1' "$@"
    exit_code="${FAKE_HERDR_RESIZE_EXIT:-0}"
    if [ "$exit_code" -ne 0 ]; then
      printf 'stub resize failure\n' >&2
      exit "$exit_code"
    fi
    printf '{"result":{"type":"pane_resized"}}\n'
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
