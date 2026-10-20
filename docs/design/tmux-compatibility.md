# tmux compatibility targets

| Status | Date |
| --- | --- |
| Accepted | 2026-10-17 |

## Supported versions

| Role | Version | Released |
| --- | --- | --- |
| Minimum supported | tmux 2.9 | 2019-03-26 |
| Current stable | tmux 3.8 | 2026-09-09 |

The plugin supports the minimum version and every later release. The minimum is a capability floor, not a distribution baseline or an age cutoff: it is the oldest release that provides every capability listed below, and `window-size` in tmux 2.9 is the highest first-appearance among them.

The versions exercised by compatibility CI are fixed in [Compatibility CI](#compatibility-ci).

## Required capabilities

| Capability | Why the plugin needs it | Oldest release that provides it |
| --- | --- | --- |
| OSC52 clipboard emission and the `Ms` terminfo extension | A completed mouse selection is copied to the outer terminal's clipboard (#123) | tmux 1.5 |
| `set-clipboard external` server option value | tmux still sends its own copies outward, but a pane-originated OSC52 sequence can no longer create or overwrite a tmux buffer (#123) | tmux 2.6 |
| `window-size` window option with the `smallest` value | Multiple clients can attach to one saved shell, and the window must take the smallest client's size (#147) | tmux 2.9 |
| `mouse` as a session option | `set-option -t <session> mouse on` turns on selection and wheel scrolling (#113) | 2.6 or earlier; not the binding constraint |
| Default root-table mouse bindings (`MouseDrag1Pane`, `WheelUpPane`) | The dedicated server keeps tmux's default bindings instead of a custom key table (#113) | 2.6 or earlier; not the binding constraint |
| Session lifecycle and the close key (`-L`, `-f`, `has-session`, `new-session -d -c`, `attach-session -t`, `bind-key -n`, session-scoped `@` options, `if-shell -F`, `detach-client`, `send-keys`) | The popup creates or reuses a session on the dedicated server, binds the configured close key to its own session, and detaches only its own client (#114, #121, #144) | 2.6 or earlier; not the binding constraint |

Two constraints decide the floor:

- `set-clipboard` is a flag with only `on` and `off` before tmux 2.6, so the `external` value does not exist there.
- `window-size` does not exist before tmux 2.9. Its default only becomes `latest` in tmux 3.1, which is why the multi-client policy is set explicitly instead of being left to the server default.

## Capability results by candidate version

Each row was produced from the release tarball built from source on macOS arm64 on 2026-10-17, by running the repository's real-server suite (`go test ./internal/popupshell/ -run Tmux`) against that binary, plus a direct probe of the required options and of the pane-originated OSC52 behaviour.

| tmux | Real-server suite | `set-clipboard external` | `window-size smallest` | Default mouse bindings | Pane OSC52 kept out of buffers |
| --- | --- | --- | --- | --- | --- |
| 2.6 | pass | accepted | invalid option | present | yes |
| 2.9 | pass | accepted | accepted | present | yes |
| 2.9a | pass | accepted | accepted | present | yes |
| 3.0a | pass | accepted | accepted | present | yes |
| 3.1c | pass | accepted | accepted | present | yes |
| 3.2a | pass | accepted | accepted | present | yes |
| 3.3a | pass | accepted | accepted | present | yes |
| 3.4 | pass | accepted | accepted | present | yes |
| 3.5a | pass | accepted | accepted | present | yes |
| 3.6b | pass | accepted | accepted | present | yes |
| 3.7c | pass | accepted | accepted | present | yes |
| 3.8 | pass | accepted | accepted | present | yes |

Every capability the plugin uses today already passes on tmux 2.6, because the multi-client size policy (#147) is not applied yet. 2.6 stays below the minimum because `window-size` is rejected there, which makes it the highest tested passing release below the floor and the boundary case the record rules out. Releases 2.7 and 2.8 sit between it and the floor.

The results were collected on macOS. Linux distributions may carry minimal patches, so a Linux job in the compatibility matrix is the authoritative check there.

The suite is run with the candidate first on `PATH`, and the option probe is these commands against a private socket directory:

```bash
tmux -L herdr-toggle-popup -f /dev/null new-session -d -s probe -c /tmp /bin/sh
tmux -L herdr-toggle-popup -f /dev/null set-option -s set-clipboard external
tmux -L herdr-toggle-popup -f /dev/null set-option -t probe mouse on
tmux -L herdr-toggle-popup -f /dev/null set-option -w -t probe window-size smallest
tmux -L herdr-toggle-popup -f /dev/null list-keys -T root
```

The pane-originated OSC52 check emits `\033]52;c;<base64>\007` from a pane with `respawn-pane`, then reads `list-buffers` with `set-clipboard external` and again with `set-clipboard on` as a control.

## Compatibility CI

The compatibility matrix must exercise two pinned versions.

| Job | Version | Covers |
| --- | --- | --- |
| Minimum | tmux 2.9 | The oldest supported behaviour |
| Current stable | tmux 3.8 | The behaviour a new user gets |

How each job obtains tmux, and how it fails when the version is missing or wrong, is owned by #146. The contract here is the two versions and that neither is left to the runner's incidental tmux.

## Unsupported versions

tmux below 2.9 is unsupported: no capability guarantee, no CI coverage, and no row above. The plugin deliberately does not add a runtime version gate, so on an unsupported version the behaviour is whatever tmux itself does.

| Situation | Handling |
| --- | --- |
| A required option or value is missing | The attach script runs each tmux command on its own and does not stop on a failure, so the failing command prints tmux's own error inside the popup and the rest of the setup still applies. |
| A user opens an issue on an unsupported version | The support path is the `doctor` command, which reports whether tmux is present, and `SECURITY.md` already asks for the tmux version. The resolution is to upgrade to a supported release. |
| CI or a contributor runs an unsupported version | The required checks exercise only the two pinned versions (#146), so an unsupported environment cannot pass them. A local run outside the matrix is not covered by the contract. |

A hard version gate is a larger change than this record requires and has no owner, so the boundary is enforced by the documented minimum and the CI matrix, not at runtime.

## Update policy

| Event | Action |
| --- | --- |
| A new tmux stable is released | Adopt it as the current stable in the same pull request that updates the CI matrix, and re-run the real-server suite against it. |
| A required capability appears in a newer release | Raise the minimum to that release, and record the capability and reason in the Decisions table. |
| The minimum no longer builds or runs on a supported CI platform | Raise the minimum to the oldest release that does, and record the platform and reason. |
| The minimum needs a security fix that cannot be worked around | Raise the minimum to the first fixed release, and record the advisory. |

This record is the source of truth for the versions. The workflow matrix and these tables must agree, and changing one without the other is a defect, the same way a CI job rename must change the branch ruleset.

## Decisions

| Decision | Basis |
| --- | --- |
| The minimum is a capability floor, not a distribution baseline | The plugin breaks on a missing capability, not on an old version number, so the oldest release that provides every capability is the correct floor. |
| Minimum tmux 2.9 | The highest first-appearance among the required capabilities is `window-size` in 2.9. tmux 2.6 already satisfies every capability the code implements today, so only the multi-client size policy raises the floor. Rejected: 2.6, which would leave the sizing policy to an implicit default. |
| Current stable tmux 3.8 | It is the newest tmux release at the time of writing and what `brew install tmux` and current packages provide. |
| Compatibility CI runs the minimum and the current stable | Two pinned points cover the oldest supported behaviour and the behaviour a new user gets, which is where a regression is most likely (#146). |
| No runtime version gate | Below-floor behaviour is documented and the floor is covered by CI, so a gate would add code without a requirement behind it. |
