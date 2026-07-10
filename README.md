# herdr-toggle-popup

A [Herdr](https://herdr.dev) plugin that toggles an overlay popup shell with one keybinding.

![](https://static.zenn.studio/user-upload/94bf4c5e9cc5-20260707.gif)

## Install

```bash
herdr plugin install maro114510/herdr-toggle-popup
```

This plugin requires `tmux` to keep the popup shell session alive while the Herdr popup pane is hidden.
Install runs a confirmed build step that verifies `tmux` is available and tries to install it when possible (`brew install tmux` on macOS/Homebrew, or root package-manager installs on Linux).
If automatic installation is not possible, install tmux yourself and rerun the plugin install:

```bash
brew install tmux              # macOS / Homebrew
```

The same build step builds `./bin/toggle-popup` from source if you have a Go toolchain, otherwise it downloads and checksum-verifies the matching prebuilt binary from GitHub Releases.

For local development, link your checkout instead, then build the binary yourself:

```bash
herdr plugin link .
sh scripts/build.sh   # or: go build -o bin/toggle-popup .
```

`sh scripts/build.sh` also checks the `tmux` dependency for local development.

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

## Session behavior

Pressing the toggle key while the popup is visible closes the Herdr popup pane completely, so no border or zoom indicator remains on screen.
The shell itself keeps running inside a named `tmux` session derived from the popup scope and entrypoint.
Pressing the toggle key again opens a fresh Herdr overlay pane and attaches it to that same tmux session.

If you want to intentionally discard a saved popup shell session, kill the matching tmux session manually with `tmux ls` and `tmux kill-session -t <session>`.

## Configuring popup size

Herdr has no way to open a popup at an absolute size, or to read a pane's current dimensions — `herdr pane resize` only supports relative, directional resizing (`--direction left|right|up|down`, `--amount FLOAT`).
Because of this, the plugin can only approximate a target size by issuing a bounded number of resize calls after opening; it cannot match an exact size or percentage the way tmux's `-w`/`-h` can.

To configure this, add a `popup_size.<entrypoint>` key to `$HERDR_PLUGIN_CONFIG_DIR/config.toml` (see "Directory-scoped popups" above for finding that directory):

```toml
popup_size.shell = "right:0.5:3 down:0.5:3"
```

The value is a space-separated list of `direction:amount:count` steps, run in order against the newly opened popup: each step calls `herdr pane resize --direction <direction> --amount <amount>` `<count>` times.
Tune the values by trial and error to approach the size you want, the same way you'd tune tmux's `-w`/`-h` per keybinding.
An entrypoint with no `popup_size` key opens exactly as it does today.

## License

[Apache License 2.0](./LICENSE)
