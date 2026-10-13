# Design: release signing, required checks, and exception policies

| Field | Value |
| --- | --- |
| Status | Accepted |
| Date | 2026-10-13 |
| Scope | Default branch, release tags, release artifacts |

This record fixes the signing, required-check, and exception policies so the implementation issues have no open choices. Issues #151, #152, #153, #135, #136, and #137 implement it.

## Protected targets

| Target | Condition | Protections |
| --- | --- | --- |
| Default branch | `~DEFAULT_BRANCH` | Deletion, force push, pull request, required checks |
| Release tags | `refs/tags/v*` | Update, deletion, force push, required checks |
| Published releases | Release objects | Immutable release |

`~DEFAULT_BRANCH` follows a default-branch rename.

## Required checks

The CI workflow reports these six contexts and all six are required.

| Context | Job |
| --- | --- |
| `shellcheck` | shellcheck |
| `go (ubuntu-latest)` | go |
| `go (macos-latest)` | go |
| `race (ubuntu-latest)` | race |
| `race (macos-latest)` | race |
| `golangci-lint` | golangci-lint |

Read the live contexts from the job names of the latest `CI` run on the default branch.

```bash
default_branch="$(gh api repos/maro114510/herdr-toggle-popup --jq .default_branch)"
run_id="$(gh run list --workflow CI --branch "${default_branch}" --limit 1 --json databaseId --jq '.[0].databaseId')"
gh api "repos/maro114510/herdr-toggle-popup/actions/runs/${run_id}/jobs" --jq '.jobs[].name' | sort -u
```

Read the workflow's jobs rather than every check run on a commit, because a commit also carries checks the `CI` workflow does not produce, such as the Dependabot graph update.

A matrix job reports one context per combination, so each combination is listed. The `Verify` and `GitHub Release` jobs run only on tags and are not required. Changing a job or a matrix dimension changes these names, so the ruleset must be updated in the same pull request. #133 keeps a single Go toolchain and leaves these six unchanged. Every new job that runs on pull requests, from #139 or #137, adds a context the ruleset must require. #151 should compare the ruleset against these workflow job names and fail or warn on drift.

## Pull request settings

| Setting | Value | Reason |
| --- | --- | --- |
| Required approvals | 0 | A single maintainer cannot approve their own pull request. The pull request exists for the record and to trigger checks |
| Code owner review | Off | `CODEOWNERS` names only the maintainer |
| Last push approval | Off | It has no effect with zero approvals |
| Dismiss stale reviews | On | It behaves correctly if approvals are added later |
| Review thread resolution | Required | No unresolved comment may be merged past |
| Allowed merge methods | `merge`, `squash`, `rebase` | Matches the current repository settings |

## Exceptions and emergency access

Normal operation places no bypass actor on the `main` ruleset or the release-tag ruleset, so nothing bypasses the pull request rule, the required checks, or the tag protections. The only elevated permission is the repository administrator's ruleset edit right, used as an emergency break-glass path.

1. Record the reason in the tracking issue.
2. Add a time-boxed bypass actor with the value `{"actor_id": 5, "actor_type": "RepositoryRole", "bypass_mode": "always"}`. The id `5` is the repository administrator role, and `always` writes an audit entry where `exempt` does not.
3. Make only the minimal change or tag push needed to recover.
4. Remove the bypass actor and read the ruleset back to confirm an empty `bypass_actors`.
5. Record the incident in the tracking issue.

Setting `enforcement` to `disabled` and back is the fallback when the bypass-actor edit itself cannot be applied, for example because the rulesets API is failing.

## Signing key management

| Field | Value |
| --- | --- |
| Owner | `Atsuki Nohira <atsuki.nohira@stelzen.dev>`, the repository maintainer |
| Purpose | Release tag signatures for this repository only |
| Algorithm | Ed25519 signing key, or RSA 4096 if the storage device requires it |
| Expiry | Two years, renewed on rotation |
| Private key | Offline media or a hardware token, protected by a passphrase, never in the repository, CI, or a GitHub secret |
| Public key | `main` at `.github/release-signing-key.asc`, also registered on the maintainer's GitHub account as a signing key |
| Fingerprint | Recorded in this record and in the release notes when #152 creates the key, so consumers can compare it out of band |
| Revocation certificate | Created at key creation and stored offline apart from the private key |
| Local Git config | `user.signingkey` and `tag.gpgsign` are set. Commit signing is not required, so `commit.gpgsign` stays off |

Registering the public key on GitHub is required for the secondary GitHub verdict below.

## Signature verification

Tags are annotated and signed. The version-bump commit needs no signature because it merges through a pull request, which keeps the key's use narrow.

```bash
git tag -s "v${VERSION}" -m "v${VERSION}"
git push origin "v${VERSION}"
```

The release workflow verifies the tag before publication.

```bash
git fetch --tags --force origin "refs/tags/${TAG_NAME}:refs/tags/${TAG_NAME}"
git fetch origin main
git show origin/main:.github/release-signing-key.asc | gpg --import
git verify-tag "${TAG_NAME}"
git merge-base --is-ancestor "$(git rev-parse "${TAG_NAME}^{commit}")" origin/main
```

The workflow takes the public key from `main` instead of the tagged commit, so a tag whose own tree carries a substituted key does not pass. `git verify-tag` then decides whether the release is published. Because it does not trust GitHub account state, an unsigned tag or a tag signed by another key fails here. The ancestry check requires the tagged commit to be reachable from `main`, which the required-check rule alone does not prove. Consumers run the same key import and verify commands, and compare the key fingerprint against the value recorded here and in the release notes.

The workflow still starts from the tag's own `push`, so the workflow file itself is read from the tagged commit. That is the gap [Limitation](#limitation-the-gate-trusts-the-tagged-commit) records. The workflow also requires GitHub's verdict as a secondary check.

```bash
tag_sha="$(gh api "repos/${GITHUB_REPOSITORY}/git/ref/tags/${TAG_NAME}" --jq '.object.sha')"
test "$(gh api "repos/${GITHUB_REPOSITORY}/git/tags/${tag_sha}" --jq '.verification.verified')" = "true"
```

Both must pass, and a failure stops the workflow before publication. The GitHub verdict is required so the tag carries GitHub's verified badge, but it is not an independent security boundary. It passes only when the public key is registered on the account, so an actor who controls the account can register a different key, and removing the key blocks every legitimate release until it returns. GitHub cannot enforce tag signatures through a ruleset, because `required_signatures` examines commits pushed to a matching ref and not the tag object's signature, so the tag ruleset omits it.

## Limitation: the gate trusts the tagged commit

The release workflow starts from the tag's `push`, so GitHub reads the workflow file from the tagged commit. A write-access actor who can push a `v*` tag can point it at a commit whose tree carries a modified workflow that drops the signature check, the key import, or the ancestry check, and the gate would then accept the result. The tag ruleset restricts updates and deletions but not creation, so it does not close that path.

The policy accepts the gap. One maintainer holds the only write access, and the gate's value is that it rejects unsigned tags, mis-signed tags, and ordinary mistakes while giving consumers provenance they can verify offline. Revisit the decision if a second writer is added. The alternatives are restricting tag creation to the repository administrator, which would also bypass the tag ruleset's checks for the administrator, or moving the release trigger onto `main` so the workflow comes from the default branch.

## Key rotation and revocation

Rotation replaces the key every two years, when the current key reaches its expiry, and on demand at any time.

1. Generate a new key with the same owner and expiry.
2. Add the new public key to `.github/release-signing-key.asc` on `main` through a pull request. A retired but still trusted key stays in the file so historical tags remain verifiable.
3. Register the new public key on the maintainer's GitHub account as a signing key.
4. Point the local `user.signingkey` at the new key and update the fingerprint in this record.
5. Optionally re-export an old public key with an extended expiry so historical tags stay verifiable.

Revocation applies when a private key is lost, stolen, or retired before expiry.

1. Import the offline revocation certificate so the key is marked revoked, and remove the key from the maintainer's GitHub account. The key is not distributed through public keyservers, so the committed public key and the advisory are the channels that matter.
2. Remove the revoked public key from `.github/release-signing-key.asc` through a pull request. `git verify-tag` trusts this file, so keeping the key would keep accepting its signatures. Git history retains it for offline verification of historical tags.
3. Rotate to a new key.
4. Re-release every affected version when the private key may have been exposed. When the key was retired without exposure, the existing tags stay valid and no re-release is needed. A published tag cannot be reused, so a re-release bumps the manifest to the next patch version through the pull request path and tags the new commit.
5. Document the revocation and the affected tags in a security advisory.

GitHub's verification record persists after a key is revoked, so old tags keep their verified badge and the advisory communicates trust.

## Release conditions

A change may become a release only when all of the following hold.

1. The version bump is prepared on a topic branch and merged to `main` through a pull request that satisfies the same pull request rule and required checks as any other change. No bypass is used.
2. The tag is an annotated tag created with the signing key and placed on a commit reachable from `main`. The release workflow enforces the reachability, because the required-check rule only proves that the tagged commit carries passing checks.
3. `herdr-plugin.toml` at the tagged commit has a `version` value equal to the tag name without the `v` prefix. The release workflow already checks this.
4. All required assets exist before publication. These are the four binaries, `checksums.txt`, artifact attestations, and a machine-readable software bill of materials.
5. The release is published as an immutable release from a draft, with every asset attached first. Publication freezes the assets and the tag, so preparing everything first prevents an incomplete published release.

The version bump now travels through a pull request, so `make release` is reworked to merge the bump first and then push the signed tag on the merged commit. The direct-push path is removed.

## Failure handling

| Situation | Handling |
| --- | --- |
| Tag signature fails | The workflow stops before publication and no release is published. The bad tag is already pushed and the tag ruleset blocks moving or deleting it. Either use the break-glass bypass to delete and re-push a correctly signed tag on the same commit, or bump the manifest to the next patch version and tag that new commit. A new tag cannot point at the old commit because the workflow requires the manifest version to match the tag |
| The tag is not reachable from `main` | The workflow's ancestry check stops before publication. Delete the tag through the break-glass bypass and re-tag the merged commit, or bump the manifest and tag the new commit |
| A required check is missing and a security fix must land | Use the break-glass bypass. The audit entry is the record |
| CI is unavailable | Prefer waiting. If a security release cannot wait, set the ruleset `enforcement` to `disabled`, restore it, and record the incident |
| The workflow fails after the tag is pushed | The tag exists. Fix the workflow and re-run it against the tag. Do not move or delete the tag |
| The signing key or the GitHub account is compromised | Revoke and rotate, publish an advisory, and re-release affected versions under new tags. Treat releases published during the exposure window as suspect, because the actor could push a tag whose commit carries its own workflow |

## Ruleset definitions

The `main` ruleset.

```json
{
  "name": "main",
  "target": "branch",
  "enforcement": "active",
  "bypass_actors": [],
  "conditions": { "ref_name": { "include": ["~DEFAULT_BRANCH"], "exclude": [] } },
  "rules": [
    { "type": "deletion" },
    { "type": "non_fast_forward" },
    {
      "type": "pull_request",
      "parameters": {
        "required_approving_review_count": 0,
        "dismiss_stale_reviews_on_push": true,
        "require_code_owner_review": false,
        "require_last_push_approval": false,
        "required_review_thread_resolution": true,
        "allowed_merge_methods": ["merge", "squash", "rebase"]
      }
    },
    {
      "type": "required_status_checks",
      "parameters": {
        "strict_required_status_checks_policy": true,
        "do_not_enforce_on_create": true,
        "required_status_checks": [
          { "context": "shellcheck" },
          { "context": "go (ubuntu-latest)" },
          { "context": "go (macos-latest)" },
          { "context": "race (ubuntu-latest)" },
          { "context": "race (macos-latest)" },
          { "context": "golangci-lint" }
        ]
      }
    }
  ]
}
```

The release-tag ruleset.

```json
{
  "name": "release tags",
  "target": "tag",
  "enforcement": "active",
  "bypass_actors": [],
  "conditions": { "ref_name": { "include": ["refs/tags/v*"], "exclude": [] } },
  "rules": [
    { "type": "update" },
    { "type": "deletion" },
    { "type": "non_fast_forward" },
    {
      "type": "required_status_checks",
      "parameters": {
        "strict_required_status_checks_policy": true,
        "required_status_checks": [
          { "context": "shellcheck" },
          { "context": "go (ubuntu-latest)" },
          { "context": "go (macos-latest)" },
          { "context": "race (ubuntu-latest)" },
          { "context": "race (macos-latest)" },
          { "context": "golangci-lint" }
        ]
      }
    }
  ]
}
```

The tag ruleset omits `creation`, because that rule lets only bypass actors create matching refs and this design keeps the bypass list empty, so `creation` would block every release tag. Creation is limited by write access, and authenticity is decided by the workflow signature gate, which trusts the tagged commit as [Limitation](#limitation-the-gate-trusts-the-tagged-commit) describes. The tag ruleset's `required_status_checks` evaluates the commit a tag points at, not the tag itself, so it requires the tagged commit to carry the six contexts and nothing more. It does not prove that the commit is on `main` or that it is the merge commit, so the release workflow's ancestry check carries that part. #152 implements both in `make release`.

## Decisions

| Decision | Basis | Rejected alternative |
| --- | --- | --- |
| A dedicated offline signing key, used only for release tags | It supports a revocation certificate and expiry | A dedicated SSH signing key, or reusing the SSH authentication key |
| `git verify-tag` decides tag authenticity | GitHub cannot enforce tag signatures through a ruleset | Relying on the tag ruleset's `required_signatures` |
| No bypass actors | Changes must go through a pull request and checks | A standing administrator bypass for release commits |
| Version bumps merge through a pull request | It keeps a single path to the default branch | Direct push, or a CI-created release commit |
| Zero required approvals | A single maintainer cannot approve their own pull request | One required approval, which forces bypass use |
| No required commit signing | It would demand a signature on every pull request commit and make merges fragile | `required_signatures` on `main` with `commit.gpgsign` on |
| Revoked keys are removed from the committed public key file | `git verify-tag` trusts that file | Keeping every historical key for verification |
| The tag `creation` rule is omitted | With no bypass actor it would block every release tag | `creation` plus an administrator bypass |
| The gate trusts the tagged commit | One maintainer holds the only write access, and the gate still rejects unsigned and mis-signed tags and gives consumers offline-verifiable provenance | Restricting tag creation to the administrator, which also bypasses the tag ruleset's checks for the administrator; moving the release trigger onto `main` |
| The workflow takes the public key from `main` | A tag's own tree must not choose the key it is verified against | Importing the key from the tagged commit |
| The release workflow checks that the tag is reachable from `main` | The tag's required-check rule only proves that the tagged commit carries passing checks | Relying on the required-check rule for reachability |
| The tag `required_signatures` rule is omitted | It cannot enforce a tag's signature and risks blocking tags whose commit is unsigned | Including it as defense in depth |
| Immutable releases are published from a draft | Publication freezes assets, so they must exist first | Publishing first and attaching assets later |

## Handoff to implementation

| Issue | What this design fixes |
| --- | --- |
| #151 | The ruleset payload, required-check names, pull request settings, and the no-bypass exception policy |
| #152 | The signing identity and key storage, the public key location, the `git verify-tag` gate, rotation and revocation, and the pull-request-based release flow |
| #153 | Immutable release through a draft with all required assets prepared first |
| #135 and #136 | Artifact attestations and a software bill of materials as required release assets |
| #137 | Its digest comparison runs as a CI job on the merge commit, so it must pass before the tag exists. Add its context to the ruleset when the job lands |
