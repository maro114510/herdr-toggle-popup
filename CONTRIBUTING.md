# Contributing

Thanks for improving herdr-toggle-popup. Contributions are accepted under the Apache License 2.0.

## Prerequisites

| Tool | Requirement |
| --- | --- |
| Go | The version named in [go.mod](./go.mod) |
| tmux | Available on PATH |
| Herdr | 0.9.0 or newer for manual checks |
| git | Any recent version |
| make | Optional, for the convenience targets |

CI also runs golangci-lint, shellcheck, and govulncheck.

## Set up a development checkout

```bash
git clone https://github.com/maro114510/herdr-toggle-popup.git
cd herdr-toggle-popup
go build ./...
```

Link the checkout and build the binary to run the plugin inside Herdr.

```bash
herdr plugin link .
sh scripts/build.sh
```

`scripts/build.sh` builds from source when Go is available and otherwise downloads a checksum-verified release binary for the version in [herdr-plugin.toml](./herdr-plugin.toml). It never installs system packages, so install tmux yourself. The [README](./README.md) covers install, keybinding, scoping, and diagnostics.

## Validate before you push

CI runs these checks on every pull request.

| Check | Command |
| --- | --- |
| Formatting | `gofmt -l .` |
| Go fixups | `go fix ./...` |
| Vet | `go vet ./...` |
| Tests | `go test -coverprofile=coverage.out ./...` |
| Vulnerabilities | `govulncheck ./...` |
| Shell scripts | `shellcheck scripts/*.sh` |
| Lint | `golangci-lint run` |

`gofmt` must print nothing. `go fix` must not modify tracked Go files. `golangci-lint` uses [.golangci.yml](./.golangci.yml).

## Project layout

| Path | Purpose |
| --- | --- |
| [main.go](./main.go) | CLI entry point and command wiring |
| [internal/](./internal) | Plugin behavior grouped by concern, each package with tests |
| [scripts/build.sh](./scripts/build.sh) | The Herdr build step |
| [herdr-plugin.toml](./herdr-plugin.toml) | Plugin manifest, actions, and panes |
| [.github/workflows/](./.github/workflows) | CI and release automation |

Add or update tests in the relevant internal package.

## Commit messages

Follow [Conventional Commits](https://www.conventionalcommits.org/). [git-cliff](./cliff.toml) derives the next version from the messages.

| Type | Release |
| --- | --- |
| feat | Yes |
| fix | Yes |
| perf | Yes |
| refactor | Yes |
| docs | Yes |
| chore | No |
| ci | No |
| test | No |
| any type with ! | Yes, breaking |

Preview the next version with `make next-version`.

## Pull requests

1. Branch from the latest main using `feat/<topic>`, `fix/<topic>`, or `docs/<topic>`.
2. Keep one change per pull request and update docs when behavior changes.
3. Run the validation checks until they pass.
4. Fill in the pull request template.
5. Open the pull request against main and keep CI green.

Respond to review with follow-up commits instead of rewriting history.

## Bug reports and feature requests

Use the repository issue forms.

| Form | What it collects |
| --- | --- |
| Bug report | Versions, environment, reproduction steps, and doctor diagnostics |
| Feature request | The problem and the expected behavior |

Run diagnostics before filing a bug.

```bash
"$HERDR_PLUGIN_ROOT/bin/toggle-popup" doctor
```

`doctor` prints versions, tool presence, configuration scope, and registry counts. It does not print shell contents.

## Keep secrets out

Issues and pull requests are public. Remove API keys, tokens, passwords, private keys, session identifiers, and private paths before submitting. Paste only the `doctor` lines you need.

## Security

Report vulnerabilities through GitHub private vulnerability reporting and never open a public issue. [SECURITY.md](./SECURITY.md) lists the supported versions and the reporting policy.
