// Package gc implements the `gc` subcommand: pruning registry entries whose pane no longer
// exists on the herdr side, e.g. because herdr crashed before emitting a pane.closed event.
package gc

import (
	"context"
	"fmt"
	"io"

	"github.com/maro114510/herdr-toggle-popup/internal/herdr"
	"github.com/maro114510/herdr-toggle-popup/internal/state"
)

// Run implements the `gc` subcommand. It reports the number of entries removed on stdout.
func Run(_ []string, stdout, stderr io.Writer) int {
	stateDir, err := state.StateDirFromEnv()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "gc: %v\n", err)
		return 1
	}

	store := state.NewStore(stateDir)
	client := herdr.NewClient()
	ctx := context.Background()

	var removed int
	if err := store.WithLock(func() error {
		removed, err = pruneOrphanedEntries(ctx, store, client)
		return err
	}); err != nil {
		_, _ = fmt.Fprintf(stderr, "gc: %v\n", err)
		return 1
	}

	_, _ = fmt.Fprintf(stdout, "gc: removed %d entr%s\n", removed, plural(removed))
	return 0
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

// pruneOrphanedEntries removes every non-hidden registry entry whose pane no longer exists on
// the herdr side. Hidden entries are skipped: hiding a popup already closes its herdr pane by
// design (see internal/toggle), so a hidden entry's registered pane never exists here, and
// toggling it again reopens its tmux session by name, not by that pane id.
func pruneOrphanedEntries(ctx context.Context, store *state.Store, client *herdr.Client) (int, error) {
	reg, err := store.Read()
	if err != nil {
		return 0, err
	}

	removed := 0
	for key, entry := range reg.Popups {
		if entry.Hidden != nil && *entry.Hidden {
			continue
		}
		if client.PaneExists(ctx, entry.PaneID) {
			continue
		}
		delete(reg.Popups, key)
		removed++
	}

	if err := store.WriteRegistry(reg); err != nil {
		return 0, err
	}
	return removed, nil
}
