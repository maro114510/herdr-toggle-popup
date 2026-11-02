#!/bin/sh
# scripts/release.sh — bump the manifest, commit, tag, and push a release.
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
last_tag="$(git describe --tags --match 'v[0-9]*' --abbrev=0 2>/dev/null || true)"
if [ -z "${last_tag}" ]; then
  printf '%s\n' 'No v* release tag found. Create the baseline tag first:' >&2
  printf '%s\n' '  git tag -a v0.1.0 -m v0.1.0' >&2
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

VERSION="${new_version}" perl -0pi -e 's/(^version\s*=\s*")[^"]+(")/$1$ENV{VERSION}$2/m' "${PLUGIN_MANIFEST}"
git add "${PLUGIN_MANIFEST}"
git commit -m "chore: release ${tag}"
git tag -a "${tag}" -m "${tag}"
git push "${REMOTE}" HEAD
git push "${REMOTE}" "refs/tags/${tag}"
