# Herdr compatibility contract

| Status | Date |
| --- | --- |
| Accepted | 2026-11-03 |

## Supported tracks

| Track | Version under test | Source | CI gate |
| --- | --- | --- | --- |
| Minimum | 0.9.3 | `min_herdr_version` in [herdr-plugin.toml](../../herdr-plugin.toml) | Required |
| Current stable | Latest stable GitHub release | Fetched on every run | Required |
| Preview | Latest GitHub prerelease | Fetched on every run | Advisory |

The manifest's `min_herdr_version` is the floor Herdr itself enforces when it links or installs the plugin. The minimum track tests exactly that value, so raising the floor moves the CI target in the same change. The stable and preview tracks move without a repository change, and each run records the tag and commit it used.

The plugin claims support only for the minimum and later releases on Linux and macOS. Preview is a prospective track: it is tested to find a breaking change before the next stable release, not because the plugin supports preview builds.

## What the contract verifies

Each track runs [scripts/herdr-contract.sh](../../scripts/herdr-contract.sh), which resolves the release, downloads its platform asset, and runs `TestHerdrContract` against it. The test starts a headless `herdr server` on the runner and drives the installed plugin.

| Step | Fails the job when |
| --- | --- |
| Release lookup and asset download | The API names no release, the asset is missing, or the download fails. |
| Version check | A stable binary does not report the resolved version, or a preview binary does not carry the commit its tag names. |
| `herdr plugin link` | The manifest is rejected, its minimum is newer than the binary, or the platform is unsupported. |
| `herdr plugin action invoke` | The manifest action is missing, fails to start, or receives no invocation context. |
| Popup startup | A second open is not refused with `ui_busy`, or the popup creates no saved tmux session. |
| Context injection | The action context has no workspace, tab, or focused cwd, or the saved session starts in another cwd. |
| Popup close and resume | Detaching the popup client does not close the popup, or the next open does not resume the same session in the same cwd. |

The popup shell resolves its saved-session identity from `HERDR_SOCKET_PATH`, `HERDR_WORKSPACE_ID`, and the focused cwd in `HERDR_PLUGIN_CONTEXT_JSON`. A popup that starts at all therefore proves Herdr injected them, and the tmux session's cwd proves the invocation context reached the plugin.

No TUI client is attached in CI. Herdr opens the popup on the server, and the test detaches the popup's tmux client to close it. That is the same shutdown path the close key triggers, so the contract does not need terminal input automation. The key binding itself is covered by the [tmux compatibility record](./tmux-compatibility.md).

The runner installs Ubuntu's tmux 3.4, the [tmux floor](./tmux-compatibility.md), and records the version it used. A tmux regression therefore shows up in the same run.

## Recording a run

Every job prints one record and appends it to the step summary and `record.txt`:

```text
track=stable version=0.9.5 tag=v0.9.5 revision=0123456789abcdef0123456789abcdef01234567 tmux=3.4
```

The revision is the commit the release tag resolves to, not the tag name. The record is written before the contract test runs, so a failed run still names the exact version and revision. Failure artifacts, kept for 14 days, add the Herdr server log and a dump of `herdr status server`, the plugin logs, and the tmux sessions.

## Failure handling

| Failure | Handling |
| --- | --- |
| Release lookup or asset download fails | The job fails. It never falls back to a Herdr on `PATH` and never turns the contract into a skipped test. |
| A downloaded binary reports another version | The job fails before the contract test runs. |
| Minimum or stable contract fails | A contract break. Fix it, or document and widen the supported surface, before the next release. |
| Preview contract fails | An early warning about an upcoming stable release. It does not change release eligibility. |

Release eligibility is decided by the required checks on `main`, listed in [release protection](./release-protection.md). This workflow is not one of those checks and never runs on a tag, so a preview failure cannot block a release. It still fails the scheduled run, because a green run would hide the regression.

## Update policy

| Event | Action |
| --- | --- |
| Raise the supported Herdr floor | Change `min_herdr_version`. The minimum track follows in the same change. |
| A new stable is released | Nothing to pin. The stable track fetches it on the next run and records it. |
| A preview regression appears | Read the failure artifact, then fix the plugin or document the unsupported behavior before it reaches stable. |
| A track cannot be fetched | Treat it as a CI failure, not as a pass. Check the release assets and the platform matrix before changing the workflow. |

## Decisions

| Decision | Basis |
| --- | --- |
| The minimum is the manifest floor, not a second pin | Herdr refuses to link a plugin whose minimum is above its own binary, so the manifest already enforces the value. Rejected: a separate version constant that can drift from it. |
| Stable and preview are fetched on every run | Testing the current release surfaces drift without a repository change, and the record keeps the run reproducible. Rejected: pinning both, which turns detection into a manual bump. |
| The contract runs against a headless server | CI has no terminal. The popup, action, and context paths work on the server, and this is the same API surface the TUI uses. Rejected: a TUI-only check that CI cannot run. |
| The test detaches the popup client instead of sending the close key | A popup is not a pane and has no input API. The close key only detaches the tmux client, so detaching it covers the same shutdown path. Rejected: key-level TUI automation, which belongs to the tmux record instead. |
| The contract is a Go test, not shell assertions | The repository already drives a real tmux server from Go tests, and typed JSON decoding keeps the context assertions explicit. Rejected: parsing Herdr JSON in shell. |
| The preview track fails its job | A red scheduled run is the early-warning signal. Labeling the track advisory in the record and keeping the workflow out of the required checks is what separates it from release eligibility. Rejected: `continue-on-error`, which hides a regression behind a green run. |
