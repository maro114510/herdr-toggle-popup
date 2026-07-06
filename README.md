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
command = "nohira.toggle-popup.toggle-shell"
description = "Toggle popup shell"
```

## License

[Apache License 2.0](./LICENSE)
