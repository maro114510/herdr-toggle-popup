// Package onpaneclosed implements the `on-pane-closed` subcommand: a behavior-equivalent
// port of on-pane-closed.sh, the pane.closed event hook.
package onpaneclosed

import (
	"fmt"
	"io"
	"os"

	"github.com/maro114510/herdr-toggle-popup/internal/state"
)

const paneIDEnvVar = "HERDR_PANE_ID"

// Run implements the `on-pane-closed` subcommand. herdr's manifest [[events]] hook fires for
// every pane close, not just this plugin's own popups, so this self-filters using
// HERDR_PANE_ID, deleting a registry entry only when it's ours. Idempotent: an unset/empty
// HERDR_PANE_ID, or a pane that was never registered, is a silent no-op.
func Run(_ []string, stdout io.Writer, stderr io.Writer) int {
	_ = stdout

	paneID := os.Getenv(paneIDEnvVar)
	if paneID == "" {
		return 0
	}

	stateDir, err := state.StateDirFromEnv()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "on-pane-closed: %v\n", err)
		return 1
	}

	store := state.NewStore(stateDir)
	if err := store.DeleteByPaneID(paneID); err != nil {
		_, _ = fmt.Fprintf(stderr, "on-pane-closed: %v\n", err)
		return 1
	}

	return 0
}
