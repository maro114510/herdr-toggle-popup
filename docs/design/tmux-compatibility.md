# tmux compatibility targets

| Status | Date |
| --- | --- |
| Accepted | 2026-10-17 |
| Revised | 2026-10-25 |

## Supported versions

| Role | Version | Released |
| --- | --- | --- |
| Minimum supported | tmux 3.4 | 2024-02-13 |
| Current stable | tmux 3.8 | 2026-09-09 |

The plugin supports the minimum version and every later release. The minimum is a capability floor, not a distribution baseline or an age cutoff: it is the oldest release that provides every capability listed below, and the OSC52 mouse-copy fix in tmux 3.4 is the highest first-appearance among them.

The versions exercised by compatibility CI are fixed in [Compatibility CI](#compatibility-ci).

## Required capabilities

| Capability | Why the plugin needs it | Oldest release that provides it |
| --- | --- | --- |
| OSC52 clipboard emission and the `Ms` terminfo extension | A completed mouse selection is copied to the outer terminal's clipboard (#123) | tmux 1.5 |
| OSC52 mouse-copy without crashing on ncurses 6.4 and later | tmux 2.9–3.3 call `tparm` after `del_curterm`, ncurses 6.4 returns `NULL`, and tmux dereferences it while emitting the copy (#154) | tmux 3.4 |
| `set-clipboard external` server option value | tmux still sends its own copies outward, but a pane-originated OSC52 sequence can no longer create or overwrite a tmux buffer (#123) | tmux 2.6 |
| `window-size` window option with the `smallest` value | Multiple clients can attach to one saved shell, and the window must take the smallest client's size (#147) | tmux 2.9 |
| `mouse` as a session option | `set-option -t <session> mouse on` turns on selection and wheel scrolling (#113) | 2.6 or earlier; not the binding constraint |
| Default root-table mouse bindings (`MouseDrag1Pane`, `WheelUpPane`) | The dedicated server keeps tmux's default bindings instead of a custom key table (#113) | 2.6 or earlier; not the binding constraint |
| Session lifecycle and the close key (`-L`, `-f`, `has-session`, `new-session -d -c`, `attach-session -t`, `bind-key -n`, session-scoped `@` options, `if-shell -F`, `detach-client`, `send-keys`) | The popup creates or reuses a session on the dedicated server, binds the configured close key to its own session, and detaches only its own client (#114, #121, #144) | 2.6 or earlier; not the binding constraint |

Three constraints decide the floor:

- `set-clipboard` is a flag with only `on` and `off` before tmux 2.6, so the `external` value does not exist there.
- `window-size` does not exist before tmux 2.9. Its default only becomes `latest` in tmux 3.1, which is why the multi-client policy is set explicitly instead of being left to the server default.
- The OSC52 mouse-copy crash exists through tmux 3.3, so 3.4 is the first release that can complete the clipboard path on Linux with ncurses 6.4.

## Capability results by candidate version

Each row came from the release tarball built from source on macOS arm64 on 2026-10-17, running `go test -run TestTmuxAttachScript ./internal/popupshell/` against that binary plus a direct probe of the required options and of the pane-originated OSC52 behaviour. The close-key assertions were added later, and the mouse-drag test was added on 2026-10-25. These macOS rows alone do not establish the floor; the Linux mouse-copy check below does.

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

tmux 2.6 passes every capability the plugin used before the multi-client size policy (#147), but it rejects `window-size`. The floor is not set by 2.6: 2.9–3.3 accept the option probes and still fail the Linux mouse-copy check below.

The results were collected on macOS. Linux distributions may carry minimal patches, so a Linux job in the compatibility matrix is the authoritative check there. The macOS result is not evidence for Linux: macOS's ncurses tolerates `tparm` after `del_curterm`, which is the difference described next.

The suite is run with the candidate first on `PATH`, and the option probe is these commands against a private socket directory:

```bash
tmux -L herdr-toggle-popup -f /dev/null new-session -d -s probe -c /tmp /bin/sh
tmux -L herdr-toggle-popup -f /dev/null set-option -s set-clipboard external
tmux -L herdr-toggle-popup -f /dev/null set-option -t probe mouse on
tmux -L herdr-toggle-popup -f /dev/null set-option -w -t probe window-size smallest
tmux -L herdr-toggle-popup -f /dev/null list-keys -T root
```

The pane-originated OSC52 check emits `\033]52;c;<base64>\007` from a pane with `respawn-pane`, then reads `list-buffers` with `set-clipboard external` and again with `set-clipboard on` as a control.

## Linux mouse-copy check

The mouse-drag test was run against release tarballs built from source on Linux (Debian bookworm, ncurses 6.4, aarch64) on 2026-10-25. It drives an SGR drag and asserts both the tmux buffer contents and the decoded OSC52 payload.

| tmux | Mouse-copy result |
| --- | --- |
| 2.9 | crash: SIGSEGV in `tty_cmd_setselection` |
| 3.0a | crash: SIGSEGV in `tty_cmd_setselection` |
| 3.3a | crash: SIGSEGV in `tty_cmd_setselection` |
| 3.4 | pass: buffer and OSC52 payload match |
| 3.8 | pass: buffer and OSC52 payload match |

tmux 2.9–3.3 call `tparm` after `del_curterm`, and ncurses 6.4 returns `NULL` for that call. tmux dereferences the result in `tty_cmd_setselection`, so the server dies on the first mouse copy that emits OSC52. tmux 3.4 switched to `tiparm` and added `NULL` checks, which removes the crash. Versions between 2.9 and 3.3 share the crashing code; 3.4 is the first release with the fix.

## Compatibility CI

The `tmux-compat` matrix in [.github/workflows/ci.yml](../../.github/workflows/ci.yml) runs the real-server suite on both supported targets.

| Matrix entry | Version | Covers |
| --- | --- | --- |
| Minimum | tmux 3.4 | The oldest supported behaviour |
| Current stable | tmux 3.8 | The behaviour a new user gets |

| Step | Fails the job on |
| --- | --- |
| Release tarball pinned by SHA-256 in the matrix | A wrong or missing download |
| Source build on `ubuntu-24.04`, then `PATH` | A build error |
| Binary path and `tmux -V` compared with the matrix | A stale or wrong tmux |
| At least ten passing real-server tests, including both named mouse-copy tests, with `HERDR_TEST_REQUIRE_TMUX=1` | A skipped, missing, or renamed test |

Both contexts are required checks; [release protection](release-protection.md) lists them.

## Unsupported versions

tmux below 3.4 is unsupported: no capability guarantee, no CI coverage, and no row above. The plugin deliberately does not add a runtime version gate, so on an unsupported version the behaviour is whatever tmux itself does.

| Situation | Handling |
| --- | --- |
| A required option or value is missing | The attach script runs each tmux command on its own and does not stop on a failure, so the failing command prints tmux's own error inside the popup and the rest of the setup still applies. |
| A mouse copy on tmux 2.9–3.3 with ncurses 6.4 | The tmux server crashes while emitting the OSC52 copy. Upgrade to tmux 3.4 or later; there is no runtime workaround because the crash is inside tmux. |
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
| A supported release has a defect that cannot be worked around | Raise the minimum to the first release that fixes it, and record the defect, platform, and verification in the Decisions table. |

This record is the source of truth for the versions. The workflow matrix and these tables must agree, and changing one without the other is a defect, the same way a CI job rename must change the branch ruleset.

## Decisions

| Decision | Basis |
| --- | --- |
| The minimum is a capability floor, not a distribution baseline | The plugin breaks on a missing capability, not on an old version number, so the oldest release that provides every capability is the correct floor. |
| Minimum tmux 3.4 | tmux 2.9–3.3 segfault on Linux with ncurses 6.4 when a mouse copy emits OSC52: `tparm` after `del_curterm` returns `NULL`, and tmux dereferences it. tmux 3.4 added `tiparm` and `NULL` checks. Rejected: keeping 2.9 with a runtime gate, which would silently remove the clipboard feature; rejected: pinning older ncurses in CI, which would hide a real user-facing crash. |
| Current stable tmux 3.8 | It is the newest tmux release at the time of writing and what `brew install tmux` and current packages provide. |
| Compatibility CI runs the minimum and the current stable | Two pinned points cover the oldest supported behaviour and the behaviour a new user gets, which is where a regression is most likely (#146). |
| CI builds tmux from the checksum-pinned release tarball | No Ubuntu release ships tmux 3.8, and a distro package cannot be pinned to an exact upstream release. Rejected: the runner image's incidental tmux. |
| The compatibility jobs run on `ubuntu-24.04` | The matrix is built from source, so `ubuntu-latest` must not move under it. Rejected: `ubuntu-latest`. |
| No runtime version gate | The floor is raised to 3.4, so a supported version cannot hit the OSC52 crash; below-floor behaviour is documented and the CI matrix enforces the floor. A gate would add code without a requirement behind it. |
