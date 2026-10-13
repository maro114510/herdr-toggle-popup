# herdr-toggle-popup

A [Herdr](https://herdr.dev) plugin that toggles a full-screen popup shell with one keybinding.

![](./docs/demo.gif)

## Install

This plugin requires Herdr 0.9.0 or newer and `tmux`.

```bash
herdr plugin install maro114510/herdr-toggle-popup
```

Install `tmux` yourself if it is not already available:

```bash
brew install tmux
```

For local development, link the checkout and build the binary:

```bash
herdr plugin link .
sh scripts/build.sh
```

## Verify a release download

Releases built with attestation support include a signed GitHub Actions build-provenance attestation. Verify a downloaded binary before running it:

```bash
gh attestation verify ./toggle-popup_darwin_arm64 \
  --repo maro114510/herdr-toggle-popup \
  --signer-workflow maro114510/herdr-toggle-popup/.github/workflows/release.yml \
  --source-ref refs/tags/vX.Y.Z
```

| Flag | Checks |
| --- | --- |
| `--repo` | This repository owns the attestation. |
| `--signer-workflow` | This plugin's release workflow signed the attestation. |
| `--source-ref` | The build ran from the release tag, which must match `version` in [`herdr-plugin.toml`](./herdr-plugin.toml). Replace `vX.Y.Z` in the command with that tag. |
| `--source-digest` | The build ran from the source commit given by `$(git rev-parse vX.Y.Z^{commit})`. |

`gh attestation verify` recomputes the file digest and rejects an altered binary. Releases published before attestation support have no attestation, so verification fails.

## Binding a key

Copy the block from [`keybindings.toml`](./keybindings.toml) into `~/.config/herdr/config.toml`:

```toml
[[keys.command]]
key = "alt+l"
type = "plugin_action"
command = "maro114510.toggle-popup.toggle-shell"
description = "Toggle popup shell"
```

## Session behavior

The plugin uses Herdr's native full-screen `popup` placement. Herdr creates the child PTY at the popup's inner size after accounting for its border, so the final prompt line remains in the visible terminal surface.

The shell runs inside a named `tmux` session on a dedicated server, so tmux's default mouse bindings stay intact. While the popup is visible, `Alt+L` is handled by that session and detaches only its current client. Herdr closes the popup and the shell keeps running. Pressing `Alt+L` again opens a fresh native popup and resumes the same session. The tmux status line stays disabled, and mouse wheel and trackpad scrolling move the shell's output history.

Herdr permits one native popup at a time. Close any existing native popup before opening this shell.

To discard a saved popup shell session, use `tmux -L herdr-toggle-popup ls` and `tmux -L herdr-toggle-popup kill-session -t <session>`. Sessions created by earlier versions stay on the default server and remain attachable.

## Directory- and tab-scoped sessions

By default, the saved tmux shell session is scoped to the workspace. To scope it to the focused directory across workspaces, create `$HERDR_PLUGIN_CONFIG_DIR/config.toml` with:

```toml
scope = "directory"
```

Use `scope = "tab"` to scope it to a particular tab within a workspace. The popup process reads the invocation context so these scopes also work with native popup panes, which do not receive `HERDR_PANE_ID`.

## Diagnostics

Run the doctor command when collecting support details:

```bash
"$HERDR_PLUGIN_ROOT/bin/toggle-popup" doctor
```

## License

[Apache License 2.0](./LICENSE)
