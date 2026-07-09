// Package toggle implements the `toggle` subcommand: open-or-toggle logic, scope keying,
// hide-by-zooming-a-sibling (so the popup stays a floating overlay and its shell session
// survives), stale-entry recovery, force modes, and best-effort resizing. It is a
// behavior-equivalent port of toggle.sh, composing the state, config, and herdr packages.
package toggle

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/maro114510/herdr-toggle-popup/internal/config"
	"github.com/maro114510/herdr-toggle-popup/internal/herdr"
	"github.com/maro114510/herdr-toggle-popup/internal/state"
)

const (
	pluginID = "maro114510.toggle-popup"

	// ModeSwitch is the default mode: another entrypoint's popup is left untouched.
	ModeSwitch = "switch"
	// ModeForceClose closes every other entrypoint's popup under the same scope before opening.
	ModeForceClose = "force-close"
	// ModeForceOpen behaves like switch but is a distinct, explicit opt-in to stacking popups.
	ModeForceOpen = "force-open"

	scopeDirectory = "directory"

	workspaceIDEnvVar = "HERDR_WORKSPACE_ID"

	msPerSecond = 1000
)

// Run implements the `toggle` subcommand: args is
// <entrypoint> [switch|force-close|force-open].
func Run(args []string, stdout, stderr io.Writer) int {
	_ = stdout

	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: toggle-popup toggle <entrypoint> [switch|force-close|force-open]")
		return 1
	}
	entrypoint := args[0]

	mode := ModeSwitch
	if len(args) > 1 {
		mode = args[1]
	}

	workspaceID := os.Getenv(workspaceIDEnvVar)
	if workspaceID == "" {
		_, _ = fmt.Fprintf(stderr, "toggle: %s must be set\n", workspaceIDEnvVar)
		return 1
	}

	switch mode {
	case ModeSwitch, ModeForceClose, ModeForceOpen:
	default:
		_, _ = fmt.Fprintf(stderr, "toggle: invalid mode: %s (expected switch, force-close, or force-open)\n", mode)
		return 1
	}

	stateDir, err := state.StateDirFromEnv()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "toggle: %v\n", err)
		return 1
	}

	cfg := config.Load()
	keyPrefix, err := scopeKeyPrefix(cfg.Scope, workspaceID)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "toggle: %v\n", err)
		return 1
	}

	store := state.NewStore(stateDir)
	client := herdr.NewClient()

	return runToggle(store, client, cfg, stderr, entrypoint, mode, keyPrefix, cfg.Scope, workspaceID)
}

// scopeKeyPrefix returns the registry key namespace for scopeMode: "workspace:<id>:" by
// default, or "directory:<focused pane cwd>:" when scopeMode is "directory". Directory scope
// errors when the focused pane's cwd cannot be determined.
func scopeKeyPrefix(scopeMode, workspaceID string) (string, error) {
	if scopeMode != scopeDirectory {
		return fmt.Sprintf("workspace:%s:", workspaceID), nil
	}
	cwd, err := focusedCwd()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("directory:%s:", cwd), nil
}

// focusedCwd returns the focused pane's cwd from the plugin invocation context, erroring when
// it cannot be determined.
func focusedCwd() (string, error) {
	cwd := herdr.ContextField("focused_pane_cwd")
	if cwd == "" {
		return "", errors.New("could not determine the focused pane's cwd")
	}
	return cwd, nil
}

// runToggle drives the hide/show/stale-recovery/open flow for one toggle invocation.
func runToggle(
	store *state.Store, client *herdr.Client, cfg config.Config, stderr io.Writer,
	entrypoint, mode, keyPrefix, scopeMode, workspaceID string,
) int {
	key := keyPrefix + entrypoint

	entry, ok, err := store.Get(key)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "toggle: %v\n", err)
		return 1
	}

	if ok {
		if client.PaneExists(entry.PaneID) {
			return toggleLivePane(store, client, stderr, key, entry)
		}
		// The registered pane no longer exists; drop the stale entry and open a fresh popup.
		if err := store.Delete(key); err != nil {
			_, _ = fmt.Fprintf(stderr, "toggle: %v\n", err)
			return 1
		}
	}

	if mode == ModeForceClose {
		closeOtherPopups(store, client, keyPrefix, key)
	}

	return openPopup(store, client, cfg, stderr, key, entrypoint, scopeMode, workspaceID)
}

// toggleLivePane hides a visible popup or re-shows a hidden one, in place. On failure (no
// sibling to hide behind, or the herdr call fails) it leaves the popup unchanged rather than
// risk orphaning a live session, and reports that to stderr. Always exits 0: the popup is
// alive either way.
func toggleLivePane(store *state.Store, client *herdr.Client, stderr io.Writer, key string, entry state.Entry) int {
	hidden := entry.Hidden != nil && *entry.Hidden

	var succeeded bool
	if hidden {
		succeeded = client.PluginPaneFocus(entry.PaneID) == nil
	} else {
		if sibling := client.TabSibling(entry.PaneID); sibling != "" {
			succeeded = client.ZoomOn(sibling) == nil
		}
	}

	if !succeeded {
		_, _ = fmt.Fprintf(stderr, "toggle: could not toggle the popup (pane %s); leaving it unchanged\n", entry.PaneID)
		return 0
	}

	if err := store.SetHidden(key, !hidden); err != nil {
		_, _ = fmt.Fprintf(stderr, "toggle: %v\n", err)
		return 1
	}
	return 0
}

// closeOtherPopups closes every other entrypoint's popup registered under keyPrefix (excluding
// excludeKey), deleting each registry entry regardless of whether the close call succeeds.
func closeOtherPopups(store *state.Store, client *herdr.Client, keyPrefix, excludeKey string) {
	reg, err := store.Read()
	if err != nil {
		return
	}
	for otherKey, entry := range reg.Popups {
		if otherKey == excludeKey || !strings.HasPrefix(otherKey, keyPrefix) {
			continue
		}
		_ = client.PluginPaneClose(entry.PaneID)
		_ = store.Delete(otherKey)
	}
}

// openPopup opens a new popup pane at the focused pane's cwd, registers it under key, and
// best-effort applies the configured popup_size steps.
func openPopup(
	store *state.Store, client *herdr.Client, cfg config.Config, stderr io.Writer,
	key, entrypoint, scopeMode, workspaceID string,
) int {
	cwd, err := focusedCwd()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "toggle: %v\n", err)
		return 1
	}

	paneID, err := client.PluginPaneOpen(pluginID, entrypoint, cwd)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "toggle: failed to open popup pane: %v\n", err)
		return 1
	}

	entry := state.Entry{
		PaneID:          paneID,
		PluginID:        pluginID,
		Entrypoint:      entrypoint,
		Scope:           scopeMode,
		WorkspaceID:     &workspaceID,
		TabID:           nil,
		CreatedAtUnixMs: time.Now().Unix() * msPerSecond,
		Hidden:          nil,
	}
	if err := store.Set(key, entry); err != nil {
		_, _ = fmt.Fprintf(stderr, "toggle: %v\n", err)
		return 1
	}

	applySize(client, cfg, entrypoint, paneID)
	return 0
}

// applySize runs the configured popup_size.<entrypoint> steps against the newly opened pane.
// Best-effort: resize failures are ignored, sizing must never fail the toggle.
func applySize(client *herdr.Client, cfg config.Config, entrypoint, paneID string) {
	steps := config.ParseSizeSteps(cfg.PopupSizeSteps(entrypoint))
	for _, step := range steps {
		for range step.Count {
			_ = client.PaneResize(paneID, step.Direction, step.Amount)
		}
	}
}
