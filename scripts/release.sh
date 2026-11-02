#!/bin/sh
# scripts/release.sh — merge the manifest bump through a pull request, then push the signed release
# tag, as docs/design/release-protection.md requires.
set -eu

PLUGIN_MANIFEST="herdr-plugin.toml"
BASE_BRANCH="main"
REMOTE="origin"

current_branch="$(git branch --show-current)"
if [ "${current_branch}" != "${BASE_BRANCH}" ]; then
  printf 'Release must run from %s, not %s.\n' "${BASE_BRANCH}" "${current_branch}" >&2
  exit 1
fi
if [ -n "$(git status --porcelain)" ]; then
  printf '%s\n' 'Working tree must be clean before release.' >&2
  git status --short >&2
  exit 1
fi

git fetch --tags "${REMOTE}"
git fetch "${REMOTE}" "${BASE_BRANCH}"
# The bump pull request has to be up to date with the base branch, because the required status
# checks are strict.
if [ "$(git rev-parse "${BASE_BRANCH}")" != "$(git rev-parse "${REMOTE}/${BASE_BRANCH}")" ]; then
  printf '%s must match %s/%s before release; run git pull --ff-only first.\n' \
    "${BASE_BRANCH}" "${REMOTE}" "${BASE_BRANCH}" >&2
  exit 1
fi
last_tag="$(git describe --tags --match 'v[0-9]*' --abbrev=0 2>/dev/null || true)"
if [ -z "${last_tag}" ]; then
  printf '%s\n' 'No v* release tag found. Create the baseline tag first:' >&2
  printf '%s\n' '  git tag -s v0.1.0 -m v0.1.0' >&2
  printf '%s\n' '  git push origin v0.1.0' >&2
  exit 1
fi

range="${last_tag}..HEAD"
if ! git log --format=%s "${range}" | grep -Eq '^(feat|fix|perf|refactor|docs)(\([^)]+\))?!?: .+|^[a-z]+(\([^)]+\))?!: .+' &&
  ! git log --format=%B "${range}" | grep -Eq '^BREAKING[ -]CHANGE: .+'; then
  printf 'No release bump commits found since %s. ci:, test:, and chore: do not trigger releases.\n' "${last_tag}" >&2
  exit 1
fi

current_version="$(sed -nE 's/^[[:space:]]*version[[:space:]]*=[[:space:]]*"([^"]+)".*/\1/p' "${PLUGIN_MANIFEST}" | head -n 1)"
new_version="$(git-cliff --bumped-version)"
new_version="${new_version#v}"
if [ "${new_version}" = "${current_version}" ]; then
  printf 'No release bump: manifest version remains %s\n' "${current_version}" >&2
  exit 1
fi

tag="v${new_version}"
if git rev-parse --verify --quiet "refs/tags/${tag}" >/dev/null; then
  printf 'Tag already exists: %s\n' "${tag}" >&2
  exit 1
fi

release_branch="chore/release-${tag}"
if git rev-parse --verify --quiet "refs/heads/${release_branch}" >/dev/null; then
  printf 'Branch already exists: %s\n' "${release_branch}" >&2
  exit 1
fi

# The bump commit carries no signature; only the tag does.
git switch --create "${release_branch}"
VERSION="${new_version}" perl -0pi -e 's/(^version\s*=\s*")[^"]+(")/$1$ENV{VERSION}$2/m' "${PLUGIN_MANIFEST}"
git add "${PLUGIN_MANIFEST}"
git commit -m "chore: release ${tag}"
git push "${REMOTE}" "${release_branch}"
gh pr create --base "${BASE_BRANCH}" --head "${release_branch}" \
  --title "chore: release ${tag}" \
  --body "Bump ${PLUGIN_MANIFEST} to ${tag}. The signed release tag follows the merge."
gh pr checks "${release_branch}" --watch --fail-fast

# Publish only after the bump is on the base branch, so the tag points at a commit the policy
# accepted.
git switch "${BASE_BRANCH}"
gh pr merge "${release_branch}" --merge --delete-branch
git pull --ff-only "${REMOTE}" "${BASE_BRANCH}"

git tag -s "${tag}" -m "${tag}"
git push "${REMOTE}" "refs/tags/${tag}"
printf 'Pushed signed tag %s. The release workflow verifies it before publishing.\n' "${tag}"
