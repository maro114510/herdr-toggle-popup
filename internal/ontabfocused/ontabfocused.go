// Package ontabfocused implements the `on-tab-focused` subcommand: herdr's [[events]]
// on = "tab.focused" hook, which fires on any focus change (e.g. sidebar navigation) that
// leaves a popup's tab without closing its pane. It hides any visible popup left behind in a
// tab other than the newly focused one, the same hide-not-kill-the-tmux-session behavior as
// toggling it directly.
package ontabfocused

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/maro114510/herdr-toggle-popup/internal/herdr"
	"github.com/maro114510/herdr-toggle-popup/internal/state"
)

const tabIDEnvVar = "HERDR_TAB_ID"

// Run implements the `on-tab-focused` subcommand. An unset/empty HERDR_TAB_ID is a silent no-op,
// matching on-pane-closed's treatment of its own env var.
func Run(_ []string, stdout, stderr io.Writer) int {
	_ = stdout

	tabID := os.Getenv(tabIDEnvVar)
	if tabID == "" {
		return 0
	}

	stateDir, err := state.StateDirFromEnv()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "on-tab-focused: %v\n", err)
		return 1
	}

	store := state.NewStore(stateDir)
	client := herdr.NewClient()
	ctx := context.Background()

	if err := store.WithLock(func() error {
		return hideEntriesOutsideFocusedTab(ctx, store, client, stderr, tabID)
	}); err != nil {
		_, _ = fmt.Fprintf(stderr, "on-tab-focused: %v\n", err)
		return 1
	}
	return 0
}

// hideEntriesOutsideFocusedTab hides every visible registry entry whose known tab_id differs
// from focusedTabID. Entries with no known tab_id (opened against a herdr response that omitted
// tab_id) are left alone: without that information there is no safe way to tell whether the
// entry belongs to the focused tab.
func hideEntriesOutsideFocusedTab(
	ctx context.Context, store *state.Store, client *herdr.Client, stderr io.Writer, focusedTabID string,
) error {
	reg, err := store.Read()
	if err != nil {
		return err
	}
	for key, entry := range reg.Popups {
		if entry.Hidden != nil && *entry.Hidden {
			continue
		}
		if entry.TabID == nil || *entry.TabID == "" || *entry.TabID == focusedTabID {
			continue
		}
		hideEntry(ctx, store, client, stderr, key, entry)
	}
	return nil
}

// hideEntry mirrors toggle.toggleLivePane: mark the entry hidden, then close its Herdr pane. On
// close failure, the hidden flag is rolled back and the entry is left visible; the failure is
// reported but does not stop the remaining entries in this run from being processed.
func hideEntry(ctx context.Context, store *state.Store, client *herdr.Client, stderr io.Writer, key string, entry state.Entry) {
	if err := store.SetHidden(key, true); err != nil {
		_, _ = fmt.Fprintf(stderr, "on-tab-focused: %v\n", err)
		return
	}
	if err := client.PluginPaneClose(ctx, entry.PaneID); err != nil {
		if rollbackErr := store.SetHidden(key, false); rollbackErr != nil {
			_, _ = fmt.Fprintf(stderr, "on-tab-focused: %v\n", rollbackErr)
			return
		}
		_, _ = fmt.Fprintf(stderr, "on-tab-focused: could not hide the popup (pane %s); leaving it visible: %v\n", entry.PaneID, err)
	}
}
