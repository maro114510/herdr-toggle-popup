# Reproducible release builds

| Status | Date |
| --- | --- |
| Accepted | 2026-11-03 |

A verifier rebuilds a release from its source archive and compares the digest with the published asset. That only means something when the release job and the rebuild produce identical bytes, so the release build fixes every input that would otherwise vary and CI compares two independent builds on every pull request.

## Fixed build inputs

| Input | Value | Why it is fixed |
| --- | --- | --- |
| Build path | `-trimpath` | The binary otherwise records the absolute path of the checkout, so two source trees at different paths differ |
| VCS stamp | `-buildvcs=false` | The release job builds from a git checkout while a verifier builds from an exported archive; stamping would make those differ for identical source |
| C toolchain | `CGO_ENABLED=0` | A host C compiler and its libraries would add versions that the source does not record |
| Targets | `darwin/amd64`, `darwin/arm64`, `linux/amd64`, `linux/arm64` | The set of published assets is exactly these four |
| Version | `-ldflags "-s -w -X main.version=<version>"` | The version is the tag without `v`; stripping and the embedded string are deterministic and carry no timestamp |
| Toolchain | The Go version `go.mod` names | The compiler version is part of the binary's build info, so a different Go release produces different bytes |
| Modules | `go.mod` and `go.sum` | The same module versions and hashes resolve for both builds |

`-trimpath` alone does not settle the VCS stamp, and a checkout differs from an exported archive for that reason. The release job builds from a checkout, but a verifier rebuilds from an archive, so both inputs must be fixed for the digests to agree.

## Verification

`scripts/reproducible-build.sh <version> <output-dir>` builds the four targets twice and leaves the first set in `<output-dir>`:

1. from the working tree, which is the checkout layout the release job uses;
2. from `git archive HEAD` extracted at a different, longer path, which is the archive layout a verifier uses.

It then compares the SHA-256 digest of each target twice-built pair, requires the expected version string in every binary, and runs `version` on the host's own target. Any mismatch prints both digests and exits non-zero. Run it from a clean checkout: uncommitted changes make the working-tree build differ from the export by design.

The [CI workflow](../../.github/workflows/ci.yml) runs the script in the `go (ubuntu-latest)` leg with version `0.0.0-ci` before any step that can rewrite the checkout, so a break fails a required check. The [release workflow](../../.github/workflows/release.yml) builds the published assets with the same script using the tag version, then checks that the `linux/amd64` binary reports that version. A mismatch stops the run before the release is published.

## Limits

- Reproducibility holds for one Go toolchain version. The Go release is recorded in the binary's build info (`go version -m`), and a verifier must use the same one; `go.mod` names a language version, so a later patch release is a different toolchain.
- Only the four targets are covered. A new target needs the same fixed inputs and a digest comparison before it is published.
- Reproducibility does not replace the tag signature or the artifact attestations described in [release-protection](./release-protection.md): those prove who built an asset, and this proves that anyone can rebuild it.
