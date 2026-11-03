# Legacy overlay and registry removal

| Status | Date |
| --- | --- |
| Accepted | 2026-11-03 |

Native Herdr popups replaced the zoomed overlay in 0.4.1. The overlay-only registry, event
handlers, maintenance commands, and resize steps stayed behind for compatibility and are removed
in 0.7.0.

## Schedule

| Release | Change |
| --- | --- |
| v0.6.0 | Announces the removal, names the affected interfaces, and documents recovery. |
| v0.7.0 | Removes the legacy overlay, registry, event-handler, and resize-step support. |

A user who stays on 0.5.x keeps the legacy behavior. A user who upgrades to 0.7.0 keeps every
saved shell and every file on disk.

## Affected interfaces

| Interface | v0.6.0 | v0.7.0 |
| --- | --- | --- |
| `toggle-popup gc` | Prunes registry entries whose pane no longer exists | Command removed |
| `toggle-popup on-pane-closed` | Deletes a registry entry for a closed pane | Command removed |
| `toggle-popup on-tab-focused` | Hides a popup left in another tab | Command removed |
| `popups.json` in `$HERDR_PLUGIN_STATE_DIR` | Read and written by the commands above and by the toggle migration guard | Not read or written; the file stays where it is |
| `popup_size.<entrypoint>` in `config.toml` | Legacy overlay resize steps | Ignored; the key stays in the file |
| Overlay placement and pane management in the Herdr client | Used only by the migration guard | Removed |
| `doctor` | Reports registry status and `popup_size` entry count | Reports neither |

Removal is not a migration. Nothing deletes `popups.json`, a saved tmux session, a Herdr pane, or
a configuration file.

## Recovery

### A legacy overlay

An overlay from a release before 0.4.1 is an ordinary Herdr plugin pane opened with
`--placement overlay`, not a tmux session.

1. Close the pane in Herdr as you would any plugin pane.
2. The tmux shell it showed keeps running on the server that holds it. Recover or remove it with
   the session steps below.

v0.6.0 closes a visible overlay when the toggle action next runs. v0.7.0 removes that migration
guard, so close a leftover overlay by hand after upgrading from 0.6.0.

### A legacy tmux session

A session created before the namespace was part of the identity carries no namespace. The plugin
never attaches to it or deletes it.

- On the dedicated server, list sessions with `tmux -L herdr-toggle-popup ls`, then
  `tmux -L herdr-toggle-popup attach -t <session>`.
- On the default server, list sessions with `tmux ls`, then `tmux attach -t <session>`.

Remove an unwanted legacy session on the server that holds it:
`tmux -L herdr-toggle-popup kill-session -t <session>` on the dedicated server, or
`tmux kill-session -t <session>` on the default server.
