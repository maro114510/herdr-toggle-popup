# herdr-toggle-popup

A [Herdr](https://herdr.dev) plugin that toggles an overlay popup shell with one keybinding.

## Install

```bash
herdr plugin install maro114510/herdr-toggle-popup
```

For local development, link your checkout instead:

```bash
herdr plugin link .
```

## Binding a key

Herdr does not auto-load plugin-shipped keybindings.
Copy the block from [`keybindings.toml`](./keybindings.toml) into your own `~/.config/herdr/config.toml`:

```toml
[[keys.command]]
key = "alt+l"
type = "plugin_action"
command = "maro114510.toggle-popup.toggle-shell"
description = "Toggle popup shell"
```

## Directory-scoped popups

By default, a popup is tracked per workspace: toggling the same entrypoint from two workspaces opens two independent popups.
To instead share a popup by the focused pane's working directory across workspaces, run `herdr plugin config-dir maro114510.toggle-popup` to find the plugin's config directory, then add a `config.toml` there:

```toml
scope = "directory"
```

With this set, toggling the same entrypoint from the same directory in any workspace opens or closes the same popup; toggling it from a different directory opens a separate one.

## Configuring popup size

Herdr has no way to open a popup at an absolute size, or to read a pane's current dimensions — `herdr pane resize` only supports relative, directional resizing (`--direction left|right|up|down`, `--amount FLOAT`).
Because of this, toggle.sh can only approximate a target size by issuing a bounded number of resize calls after opening; it cannot match an exact size or percentage the way tmux's `-w`/`-h` can.

To configure this, add a `popup_size.<entrypoint>` key to `$HERDR_PLUGIN_CONFIG_DIR/config.toml` (see "Directory-scoped popups" above for finding that directory):

```toml
popup_size.shell = "right:0.5:3 down:0.5:3"
```

The value is a space-separated list of `direction:amount:count` steps, run in order against the newly opened popup: each step calls `herdr pane resize --direction <direction> --amount <amount>` `<count>` times.
Tune the values by trial and error to approach the size you want, the same way you'd tune tmux's `-w`/`-h` per keybinding.
An entrypoint with no `popup_size` key opens exactly as it does today.

## License

[Apache License 2.0](./LICENSE)
