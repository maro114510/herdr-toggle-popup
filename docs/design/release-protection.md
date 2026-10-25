# Release signing, required checks, and exception policies

| Status | Date |
| --- | --- |
| Accepted | 2026-10-13 |

## Protected targets

| Target | Condition | Protection |
| --- | --- | --- |
| Default branch | `~DEFAULT_BRANCH` | Deletion, force push, pull request, required checks |
| Release tags | `refs/tags/v*` | Update, deletion, force push, required checks |
| Published releases | Release objects | Immutable release |

## Required checks

These eight `CI` contexts are required on a pull request to the default branch.

| Context | Job |
| --- | --- |
| `shellcheck` | `shellcheck` |
| `go (ubuntu-latest)` | `go` |
| `go (macos-latest)` | `go` |
| `tmux-compat (3.4)` | `tmux-compat` |
| `tmux-compat (3.8)` | `tmux-compat` |
| `race (ubuntu-latest)` | `race` |
| `race (macos-latest)` | `race` |
| `golangci-lint` | `golangci-lint` |

```bash
default_branch="$(gh api repos/maro114510/herdr-toggle-popup --jq .default_branch)"
run_id="$(gh run list --workflow CI --branch "${default_branch}" --limit 1 --json databaseId --jq '.[0].databaseId')"
gh api "repos/maro114510/herdr-toggle-popup/actions/runs/${run_id}/jobs" --jq '.jobs[].name' | sort -u
```

- Read the names from these jobs, not from every check run on a commit.
- A `CI` job or matrix change renames a context, so change the ruleset in the same pull request.
- `CI` has one Go toolchain and no version matrix.

## Pull request settings

| Setting | Value | Reason |
| --- | --- | --- |
| Required approvals | 0 | One maintainer cannot approve their own pull request |
| Code owner review | Off | `CODEOWNERS` names only the maintainer |
| Last push approval | Off | No effect while approvals are zero |
| Dismiss stale reviews | On | Correct once approvals exist |
| Review thread resolution | Required | No unresolved comment may be merged past |
| Allowed merge methods | `merge`, `squash`, `rebase` | Matches the repository settings |

## Exception access

Normal operation has no bypass actor on either ruleset. The administrator's ruleset edit right is the emergency path.

1. Record the reason in the tracking issue.
2. Apply `{"actor_id": 5, "actor_type": "RepositoryRole", "bypass_mode": "always"}`. Role `5` is the administrator, and `always` records an audit entry.
3. Make only the smallest change that recovers service.
4. Remove the actor and read the ruleset back with an empty `bypass_actors`.
5. Record the incident in the tracking issue.

Rule enforcement off and back on is the fallback when the API cannot be edited.

## Signing key

| Field | Value |
| --- | --- |
| Owner | `Atsuki Nohira <atsuki.nohira@stelzen.dev>` |
| Purpose | Release tags only |
| Algorithm | Ed25519, or RSA 4096 for a device that requires it |
| Expiry | Two years |
| Private key | Offline media or a hardware token, passphrase protected, never in the repository, CI, or a secret |
| Public key | `main` at `.github/release-signing-key.asc`, and on the maintainer's GitHub account |
| Fingerprint | Recorded here and in the release notes |
| Revocation certificate | Stored offline apart from the private key |
| Local Git config | `user.signingkey` and `tag.gpgsign` set, commit signing off |

## Tag verification

Tags are annotated and signed. The version bump commit merges through a pull request and needs no signature.

```bash
git tag -s "v${VERSION}" -m "v${VERSION}"
git push origin "v${VERSION}"
```

The workflow runs these before publishing.

```bash
git fetch --tags --force origin "refs/tags/${TAG_NAME}:refs/tags/${TAG_NAME}"
git fetch origin main
git show origin/main:.github/release-signing-key.asc | gpg --import
git verify-tag "${TAG_NAME}"
git merge-base --is-ancestor "$(git rev-parse "${TAG_NAME}^{commit}")" origin/main
```

- The public key comes from `main`, so a tag cannot choose the key that verifies it.
- `git verify-tag` ignores GitHub account state, so an unsigned tag or a foreign key fails.
- The ancestry check requires the tagged commit to be reachable from `main`.
- Consumers import the key from `main`, verify the tag, and compare the fingerprint out of band.

```bash
tag_sha="$(gh api "repos/${GITHUB_REPOSITORY}/git/ref/tags/${TAG_NAME}" --jq '.object.sha')"
test "$(gh api "repos/${GITHUB_REPOSITORY}/git/tags/${tag_sha}" --jq '.verification.verified')" = "true"
```

GitHub's own verdict on the tag is also required, but it needs the key on the account and is not a security boundary. A ruleset cannot enforce a tag signature, so the tag ruleset omits `required_signatures`.

### Limitation

GitHub reads the workflow file from the tagged commit, so a write access actor can tag a commit whose workflow drops these checks.

This gap is accepted. One maintainer holds the only write access. If a second writer is added, restrict tag creation to the administrator or move the release trigger onto `main`.

## Rotation and revocation

Rotation happens every two years on expiry, and on demand.

1. Generate a new key with the same owner and expiry.
2. Add the new public key to `.github/release-signing-key.asc` on `main` through a pull request, keeping a retired but trusted key so historical tags stay verifiable.
3. Register the new public key on the maintainer's GitHub account.
4. Point `user.signingkey` at the new key and update the fingerprint here.
5. Extend the expiry of an old key and re-export it if historical tags must stay verifiable.

Revocation covers a lost, stolen, or retired key.

1. Import the revocation certificate and remove the key from the account. The committed file and the advisory are the channels that matter.
2. Remove the revoked key from `.github/release-signing-key.asc` through a pull request, because `git verify-tag` trusts this file. Git history retains it.
3. Rotate to a new key.
4. Re-release every affected version when the key may have been exposed.
5. Document the revocation and the affected tags in a security advisory.

GitHub keeps the verified badge after a revocation, so the advisory changes consumer trust.

## Release conditions

A release may be published only when all of these hold.

1. The version bump merges to `main` through a pull request that satisfies the same rule and checks as any other change, with no bypass.
2. The tag is annotated, signed with the signing key, and placed on a commit reachable from `main`.
3. `herdr-plugin.toml` at the tagged commit has a `version` equal to the tag name without the leading `v`.
4. Four binaries, `checksums.txt`, artifact attestations, and a machine readable software bill of materials all exist.
5. The release is published from a draft as an immutable release with every asset attached first.

`make release` merges the bump first and then pushes the signed tag, replacing the direct push path.

## Failure handling

| Situation | Handling |
| --- | --- |
| Tag signature fails | Stop before publication. Delete the pushed tag through the exception path and re-tag the same commit, or bump the manifest |
| Tag not reachable from `main` | The ancestry check stops the workflow. Delete the tag and re-tag the merged commit |
| Required check missing, security fix must land | Use the exception path |
| CI unavailable | Wait, unless a release cannot. Turn rule enforcement off, restore it, and record the incident |
| Workflow fails after the tag is pushed | Fix the workflow and re-run it against the tag. Never move or delete the tag |
| Key or account compromised | Revoke and rotate, publish an advisory, and treat releases in the exposure window as suspect |

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
          { "context": "tmux-compat (3.4)" },
          { "context": "tmux-compat (3.8)" },
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
          { "context": "tmux-compat (3.4)" },
          { "context": "tmux-compat (3.8)" },
          { "context": "race (ubuntu-latest)" },
          { "context": "race (macos-latest)" },
          { "context": "golangci-lint" }
        ]
      }
    }
  ]
}
```

| Rule | Reason |
| --- | --- |
| `creation` and `required_signatures` omitted | A creation rule with no bypass actor blocks every release tag, and `required_signatures` examines commits and not the tag object |

## Decisions

| Decision | Basis |
| --- | --- |
| Dedicated offline signing key, used only for release tags | Supports a revocation certificate and expiry |
| `git verify-tag` decides tag authenticity | A ruleset cannot enforce a tag signature |
| No standing bypass actor | Changes must pass the pull request rule and the required checks |
| The public key comes from `main` | A tag must not choose the key that verifies it |
| Immutable releases are published from a draft | Publication freezes the assets |
