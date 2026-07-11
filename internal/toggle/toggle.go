// Package toggle implements the `toggle` subcommand: open-or-toggle logic, scope keying,
// close-to-hide behavior backed by a tmux session, stale-entry recovery, force modes, and
// best-effort resizing. It composes the state, config, and herdr packages.
package toggle

import (
	"context"
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
	// The Herdr client applies a per-command timeout to this root invocation context.
	ctx := context.Background()

	return runToggle(ctx, store, client, cfg, stderr, entrypoint, mode, keyPrefix, cfg.Scope, workspaceID)
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
	ctx context.Context, store *state.Store, client *herdr.Client, cfg config.Config, stderr io.Writer,
	entrypoint, mode, keyPrefix, scopeMode, workspaceID string,
) int {
	key := keyPrefix + entrypoint
	var resizePaneID string

	var code int
	// WithLock releases the registry lock as soon as this callback returns.
	// Keep only the state-dependent pane operations inside; cosmetic sizing runs after unlock.
	if err := store.WithLock(func() error {
		code, resizePaneID = runToggleLocked(ctx, store, client, stderr, key, entrypoint, mode, keyPrefix, scopeMode, workspaceID)
		return nil
	}); err != nil {
		_, _ = fmt.Fprintf(stderr, "toggle: %v\n", err)
		return 1
	}
	if code == 0 && resizePaneID != "" {
		applySize(ctx, client, cfg, entrypoint, resizePaneID)
	}
	return code
}

// runToggleLocked drives the state-dependent toggle flow while the registry lock is held.
// It returns the opened pane id when post-registration sizing should run after unlock.
func runToggleLocked(
	ctx context.Context, store *state.Store, client *herdr.Client, stderr io.Writer,
	key, entrypoint, mode, keyPrefix, scopeMode, workspaceID string,
) (int, string) {
	entry, ok, err := store.Get(key)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "toggle: %v\n", err)
		return 1, ""
	}

	if ok {
		if entry.Hidden != nil && *entry.Hidden {
			return openPopupLocked(ctx, store, client, stderr, key, entrypoint, scopeMode, workspaceID)
		}
		if client.PaneExists(ctx, entry.PaneID) {
			return toggleLivePane(ctx, store, client, stderr, key, entry), ""
		}
		// The registered pane no longer exists; drop the stale entry and open a fresh popup.
		if err := store.Delete(key); err != nil {
			_, _ = fmt.Fprintf(stderr, "toggle: %v\n", err)
			return 1, ""
		}
	}

	if mode == ModeForceClose {
		closeOtherPopups(ctx, store, client, keyPrefix, key)
	}

	return openPopupLocked(ctx, store, client, stderr, key, entrypoint, scopeMode, workspaceID)
}

// toggleLivePane hides a visible popup by marking it hidden and closing the Herdr pane. The
// shell session survives in tmux; the Herdr pane must disappear completely so no border or zoom
// indicator remains. On close failure, the hidden flag is rolled back and the live pane is left
// unchanged.
func toggleLivePane(ctx context.Context, store *state.Store, client *herdr.Client, stderr io.Writer, key string, entry state.Entry) int {
	if err := store.SetHidden(key, true); err != nil {
		_, _ = fmt.Fprintf(stderr, "toggle: %v\n", err)
		return 1
	}
	if err := client.PluginPaneClose(ctx, entry.PaneID); err != nil {
		if rollbackErr := store.SetHidden(key, false); rollbackErr != nil {
			_, _ = fmt.Fprintf(stderr, "toggle: %v\n", rollbackErr)
			return 1
		}
		_, _ = fmt.Fprintf(stderr, "toggle: could not hide the popup (pane %s); leaving it visible: %v\n", entry.PaneID, err)
		return 0
	}
	return 0
}

// closeOtherPopups closes every other entrypoint's popup registered under keyPrefix (excluding
// excludeKey), deleting each registry entry regardless of whether the close call succeeds.
func closeOtherPopups(ctx context.Context, store *state.Store, client *herdr.Client, keyPrefix, excludeKey string) {
	reg, err := store.Read()
	if err != nil {
		return
	}
	for otherKey, entry := range reg.Popups {
		if otherKey == excludeKey || !strings.HasPrefix(otherKey, keyPrefix) {
			continue
		}
		_ = client.PluginPaneClose(ctx, entry.PaneID)
		_ = store.Delete(otherKey)
	}
}

// openPopupLocked opens a new popup pane at the focused pane's cwd and registers it under key.
// The caller must hold the registry lock, then run cosmetic sizing after unlock.
func openPopupLocked(
	ctx context.Context, store *state.Store, client *herdr.Client, stderr io.Writer,
	key, entrypoint, scopeMode, workspaceID string,
) (int, string) {
	cwd, err := focusedCwd()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "toggle: %v\n", err)
		return 1, ""
	}

	paneID, err := client.PluginPaneOpen(ctx, pluginID, entrypoint, cwd)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "toggle: failed to open popup pane: %v\n", err)
		return 1, ""
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
		_ = client.PluginPaneClose(ctx, paneID)
		_, _ = fmt.Fprintf(stderr, "toggle: %v\n", err)
		return 1, ""
	}

	return 0, paneID
}

// applySize runs the configured popup_size.<entrypoint> steps against the newly opened pane.
// Best-effort: resize failures are ignored, sizing must never fail the toggle.
func applySize(ctx context.Context, client *herdr.Client, cfg config.Config, entrypoint, paneID string) {
	steps := config.ParseSizeSteps(cfg.PopupSizeSteps(entrypoint))
	for _, step := range steps {
		for range step.Count {
			_ = client.PaneResize(ctx, paneID, step.Direction, step.Amount)
		}
	}
}
