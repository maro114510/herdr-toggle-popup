# Legacy overlay removal release guidance

| Status | Date |
| --- | --- |
| Announced | 2026-11-03 |

Legacy overlay support is deprecated in v0.6.0 and removed in v0.7.0. This page is the release guidance for that window: it names the affected interfaces and explains how to recover a legacy overlay and a legacy tmux session separately.

The plugin never deletes a legacy tmux session automatically. In v0.6.0 the toggle action closes a visible legacy overlay pane and may remove a stale registry entry, but it leaves the `popups.json` file and every tmux session in place. Nothing below runs on its own; every removal step after v0.7.0 is manual.

## Release schedule

| Release | Legacy overlay support | What to do |
| --- | --- | --- |
| v0.6.0 | Announced and deprecated. The legacy commands still run, and `popup_size` and `popups.json` are still read. | Start migrating now. |
| v0.7.0 | Support ends. The legacy commands and `popup_size` are removed, and the `popups.json` registry is no longer read or written. | Finish the migration before upgrading. Existing files and sessions are left in place. |

## Affected interfaces

| Interface | v0.6.0 | v0.7.0 | Migration |
| --- | --- | --- | --- |
| `toggle-popup gc` | Available; prunes stale `popups.json` entries. | Removed; exits as an unknown command. | None. Native popups keep their state in tmux, so there is no registry to prune. |
| `toggle-popup on-pane-closed` | Available as the legacy `pane.closed` event handler. | Removed. | None. A native popup has no pane ID and emits no pane lifecycle event. |
| `toggle-popup on-tab-focused` | Available as the legacy `tab.focused` event handler. | Removed. | None. The close key closes the popup. |
| `toggle-popup doctor` | Prints the `state:` line and the `popup_size entries` count. | Prints neither; it still prints the effective `popup size:`. | None. |
| `popup_size.<entrypoint>` or `[popup_size]` | Read but not used for native popup dimensions. | Ignored. | Set `width` and `height` in `$HERDR_PLUGIN_CONFIG_DIR/config.toml`. |
| `HERDR_PLUGIN_STATE_DIR` / `popups.json` | Read by `toggle`, `doctor`, `gc`, and the event handlers. | Never read or written; an existing file is left in place. | None. Remove the file by hand only if you want to. |

`toggle`, `popup-shell`, `doctor`, and `version` remain. Native popup behavior, saved tmux shells, and the `scope`, `close_key`, `width`, and `height` settings are unchanged.

## Migrate before v0.7.0

1. Replace `popup_size.<entrypoint>`, including a `[popup_size]` table, with `width` and `height` in `$HERDR_PLUGIN_CONFIG_DIR/config.toml`. The [README popup size section](../README.md#popup-size) documents both values.
2. Remove calls to `toggle-popup gc`, `toggle-popup on-pane-closed`, and `toggle-popup on-tab-focused` from scripts, tmux bindings, or Herdr event hooks. Native popups need none of them.
3. Close any visible legacy overlay, using the procedure below if the v0.6.0 toggle action does not close it.
4. Open the popup once with the current version. It creates or reuses the namespaced tmux session for the current context.
5. Move work from a legacy tmux session to the native popup, or keep the legacy session until its work is done. The plugin never removes it for you.
6. Leave `popups.json` in place unless you want to remove it yourself. After v0.7.0 the plugin never reads it.

## Recover a legacy overlay

A legacy overlay is a Herdr plugin pane that versions before 0.4.1 opened with `--placement overlay` and recorded in `popups.json`. It is separate from the tmux session that held its shell.

In v0.6.0, running the toggle action closes a visible legacy overlay instead of opening the native popup. Run the toggle again to open the native popup. If the recorded pane no longer exists, v0.6.0 removes the stale entry and opens the native popup. In v0.7.0 that guard is gone, so recover a leftover overlay by hand.

1. Locate the registry. The plugin receives its state directory as `HERDR_PLUGIN_STATE_DIR`, and the registry is `popups.json` inside it. If the variable is unset in your shell, substitute the state directory Herdr assigned to the plugin.
2. List the recorded panes and whether each one is visible:

   ```bash
   jq -r '.popups | to_entries[] | [.key, .value.pane_id, (.value.hidden // false)] | @tsv' \
     "$HERDR_PLUGIN_STATE_DIR/popups.json"
   ```

   A visible entry has `hidden` absent or `false`. A hidden entry was already closed in Herdr, so it needs no pane recovery.
3. Check whether the pane still exists:

   ```bash
   herdr pane get <pane-id>
   ```

4. Close it through Herdr:

   ```bash
   herdr plugin pane close <pane-id>
   ```

5. Confirm it is gone. `herdr pane get <pane-id>` exits non-zero once the pane no longer exists.
6. Closing the overlay does not close its tmux session. Use [Recover a legacy tmux session](#recover-a-legacy-tmux-session) if you need that shell.

Removing the `popups.json` entry is optional. The plugin never reads the registry after v0.7.0. If you remove it, back up the file first and edit it only after the pane is gone.

## Recover a legacy tmux session

A legacy tmux session is a saved shell created before the Herdr namespace was part of its identity. It may live on the dedicated `herdr-toggle-popup` server or on tmux's default server. The plugin never attaches to or deletes one, and v0.6.0 and v0.7.0 do not change that.

1. List the sessions on the server that holds the shell:

   ```bash
   tmux -L herdr-toggle-popup ls
   ```

   and, for the default server:

   ```bash
   tmux ls
   ```

   A `no server running` error means there are no sessions on that server.

2. Attach to the one you want:

   ```bash
   tmux -L herdr-toggle-popup attach -t <session>
   ```

   or:

   ```bash
   tmux attach -t <session>
   ```

3. Move any work you need into a native popup. Open the popup with the bound key; it creates or reuses the namespaced session for the current context. You can also copy files or commands out of the old shell.
4. Detach without stopping the shell with `tmux detach-client` or the tmux prefix followed by `d` (`C-b d` by default).
5. Remove the legacy session only after its work is done and you are sure nothing else uses it:

   ```bash
   tmux -L herdr-toggle-popup kill-session -t <session>
   ```

   or:

   ```bash
   tmux kill-session -t <session>
   ```

A detached session, an old creation time, or a session with no client is not proof that its work has finished. The plugin has no automatic TTL and never deletes a legacy session.

## Confirm the migration

- `toggle-popup doctor` still reports the effective popup size and the saved-session identity; in v0.7.0 it no longer prints a `state:` line or a `popup_size entries` count.
- `toggle-popup gc`, `toggle-popup on-pane-closed`, and `toggle-popup on-tab-focused` are unknown commands in v0.7.0.
- The popup opens and reconnects to its saved shell across close and reopen.
