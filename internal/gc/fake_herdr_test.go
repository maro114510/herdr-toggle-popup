package gc

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const (
	fakeHerdrExecPerm = 0o700
	binPathEnvVar     = "HERDR_BIN_PATH"
)

// fakeHerdrScript stands in for the real herdr CLI's `pane get <id>` response: it fails
// (pane does not exist) for any pane id listed, space-separated, in
// $STUB_HERDR_MISSING_PANE_IDS, and succeeds for every other pane id.
const fakeHerdrScript = `#!/usr/bin/env bash
set -euo pipefail

case "$1 $2" in
  "pane get")
    pane_id="$3"
    for missing in ${STUB_HERDR_MISSING_PANE_IDS:-}; do
      if [ "$missing" = "$pane_id" ]; then
        printf 'stub get failure\n' >&2
        exit 1
      fi
    done
    printf '{"result":{"pane":{"pane_id":"%s"}}}\n' "$pane_id"
    ;;
  *)
    printf 'stub herdr: unhandled args: %s\n' "$*" >&2
    exit 99
    ;;
esac
`

// newFakeHerdr writes the fake herdr script to a temp dir and points HERDR_BIN_PATH at it.
// Callers set STUB_HERDR_MISSING_PANE_IDS via t.Setenv before invoking Run.
func newFakeHerdr(t *testing.T) {
	t.Helper()

	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found, skipping fake-herdr test")
	}

	dir := t.TempDir()
	bin := filepath.Join(dir, "herdr")
	if err := os.WriteFile(bin, []byte(fakeHerdrScript), fakeHerdrExecPerm); err != nil {
		t.Fatal(err)
	}

	t.Setenv(binPathEnvVar, bin)
}
