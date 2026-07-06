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

Herdr does not auto-load plugin-shipped keybindings. Copy the block from
[`keybindings.toml`](./keybindings.toml) into your own
`~/.config/herdr/config.toml`:

```toml
[[keys.command]]
key = "alt+l"
type = "plugin_action"
command = "maro114510.toggle-popup.toggle-shell"
description = "Toggle popup shell"
```

## Directory-scoped popups

By default, a popup is tracked per workspace: toggling the same entrypoint
from two workspaces opens two independent popups. To instead share a popup
by the focused pane's working directory across workspaces, run
`herdr plugin config-dir maro114510.toggle-popup` to find the plugin's
config directory, then add a `config.toml` there:

```toml
scope = "directory"
```

With this set, toggling the same entrypoint from the same directory in any
workspace opens or closes the same popup; toggling it from a different
directory opens a separate one.

## License

[Apache License 2.0](./LICENSE)
