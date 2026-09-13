// Package toggle opens the shell in a native full-screen Herdr popup.
package toggle

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/maro114510/herdr-toggle-popup/internal/config"
	"github.com/maro114510/herdr-toggle-popup/internal/herdr"
	"github.com/maro114510/herdr-toggle-popup/internal/state"
)

const (
	pluginID          = "maro114510.toggle-popup"
	stateDirEnvVar    = "HERDR_PLUGIN_STATE_DIR"
	workspaceIDEnvVar = "HERDR_WORKSPACE_ID"
)

// Run opens a native Herdr popup for entrypoint. While it is visible, Herdr routes Alt+L to the
// attached tmux client; popupshell binds that key to detach only the current client, which closes
// the popup while keeping its named tmux session alive. Once closed, Alt+L reaches this action
// again and reopens the same scoped session.
func Run(args []string, stdout, stderr io.Writer) int {
	_ = stdout
	if len(args) != 1 || args[0] == "" {
		_, _ = fmt.Fprintln(stderr, "usage: toggle-popup toggle <entrypoint>")
		return 1
	}

	cwd := herdr.ContextField("focused_pane_cwd")
	if cwd == "" {
		_, _ = fmt.Fprintln(stderr, "toggle: could not determine the focused pane's cwd")
		return 1
	}

	ctx := context.Background()
	client := herdr.NewClient()
	closedLegacy, err := closeVisibleLegacyOverlay(ctx, client, args[0], cwd)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "toggle: %v\n", err)
		return 1
	}
	if closedLegacy {
		return 0
	}
	if err := client.PluginPopupOpen(ctx, pluginID, args[0], cwd); err != nil {
		_, _ = fmt.Fprintf(stderr, "toggle: failed to open popup: %v\n", err)
		return 1
	}
	return 0
}

// closeVisibleLegacyOverlay is a one-way migration guard. Versions before 0.4.1 opened a
// zoomed overlay and recorded its pane ID. If such an overlay is still visible when the updated
// action first runs, close it instead of opening a native popup on top of it. Native popups are
// not added to this registry.
func closeVisibleLegacyOverlay(ctx context.Context, client *herdr.Client, entrypoint, cwd string) (bool, error) {
	stateDir := os.Getenv(stateDirEnvVar)
	if stateDir == "" {
		return false, nil
	}

	key, err := legacyStateKey(entrypoint, cwd)
	if err != nil {
		return false, fmt.Errorf("could not determine legacy popup scope: %w", err)
	}
	store := state.NewStore(stateDir)
	entry, found, err := store.Get(key)
	if err != nil {
		return false, err
	}
	if !legacyEntryIsVisible(entrypoint, entry, found) {
		return false, nil
	}
	return closeLegacyOverlay(ctx, client, store, key, entry)
}

func legacyEntryIsVisible(entrypoint string, entry state.Entry, found bool) bool {
	return found && (entry.Hidden == nil || !*entry.Hidden) && entry.PluginID == pluginID && entry.Entrypoint == entrypoint
}

func closeLegacyOverlay(ctx context.Context, client *herdr.Client, store *state.Store, key string, entry state.Entry) (bool, error) {
	if !client.PaneExists(ctx, entry.PaneID) {
		if err := store.Delete(key); err != nil {
			return false, err
		}
		return false, nil
	}
	if err := store.SetHidden(key, true); err != nil {
		return false, err
	}
	if err := client.PluginPaneClose(ctx, entry.PaneID); err != nil {
		if rollbackErr := store.SetHidden(key, false); rollbackErr != nil {
			return false, fmt.Errorf("could not close legacy overlay pane %s: %w", entry.PaneID, errors.Join(err, fmt.Errorf("could not restore its visible state: %w", rollbackErr)))
		}
		return false, fmt.Errorf("could not close legacy overlay pane %s: %w", entry.PaneID, err)
	}
	return true, nil
}

func legacyStateKey(entrypoint, cwd string) (string, error) {
	scope := config.Load().Scope
	if !config.IsValidScope(scope) {
		scope = "workspace"
	}
	switch scope {
	case "directory":
		return fmt.Sprintf("directory:%s:%s", cwd, entrypoint), nil
	case "tab":
		workspaceID, err := workspaceIDFromContext()
		if err != nil {
			return "", err
		}
		tabID := herdr.ContextField("tab_id")
		if tabID == "" {
			return "", errors.New("could not determine the focused tab's id")
		}
		return fmt.Sprintf("tab:%s:%s:%s", workspaceID, tabID, entrypoint), nil
	default:
		workspaceID, err := workspaceIDFromContext()
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("workspace:%s:%s", workspaceID, entrypoint), nil
	}
}

func workspaceIDFromContext() (string, error) {
	workspaceID := os.Getenv(workspaceIDEnvVar)
	if workspaceID == "" {
		workspaceID = herdr.ContextField("workspace_id")
	}
	if workspaceID == "" {
		return "", fmt.Errorf("%s must be set", workspaceIDEnvVar)
	}
	return workspaceID, nil
}
