#!/bin/sh
# scripts/verify-release-tag.sh — refuse a release tag that is not signed by a trusted key or whose
# commit is not on the base branch, as docs/design/release-protection.md requires.
#
# Usage: sh scripts/verify-release-tag.sh <tag>
set -eu

REMOTE="${REMOTE:-origin}"
BASE_BRANCH="${BASE_BRANCH:-main}"
KEY_PATH="${KEY_PATH:-.github/release-signing-key.asc}"

tag="${1:-}"
if [ -z "${tag}" ]; then
  echo "usage: verify-release-tag.sh <tag>" >&2
  exit 2
fi

git fetch --tags --force "${REMOTE}" "refs/tags/${tag}:refs/tags/${tag}"
git fetch "${REMOTE}" "${BASE_BRANCH}"
base_ref="${REMOTE}/${BASE_BRANCH}"

workspace="$(mktemp -d)"
cleanup() {
  gpgconf --kill all >/dev/null 2>&1 || true
  rm -rf "${workspace}"
}
trap cleanup EXIT HUP INT TERM

key_file="${workspace}/release-signing-key.asc"
if ! git show "${base_ref}:${KEY_PATH}" >"${key_file}" 2>/dev/null; then
  cat >&2 <<EOF
verify-release-tag.sh: ${base_ref} has no ${KEY_PATH}.
Commit the release signing public key to ${BASE_BRANCH} before publishing; see
docs/design/release-protection.md "Signing key".
EOF
  exit 1
fi

# Verify against a throwaway keyring: a key installed on this machine must never stand in for a key
# committed to the base branch.
GNUPGHOME="${workspace}/gnupg"
mkdir -m 700 "${GNUPGHOME}"
export GNUPGHOME
if ! gpg --batch --quiet --import "${key_file}"; then
  echo "verify-release-tag.sh: could not import ${KEY_PATH}." >&2
  exit 1
fi

# An unsigned tag, a tag signed by a key outside the committed keyring, and a corrupted signature
# all fail here.
if ! git verify-tag "${tag}"; then
  echo "verify-release-tag.sh: ${tag} is not signed by a key in ${KEY_PATH}." >&2
  exit 1
fi

tag_commit="$(git rev-parse "${tag}^{commit}")"
if ! git merge-base --is-ancestor "${tag_commit}" "${base_ref}"; then
  echo "verify-release-tag.sh: ${tag} points at ${tag_commit}, which is not reachable from ${base_ref}." >&2
  exit 1
fi

echo "verify-release-tag.sh: ${tag} is signed by a trusted key and reachable from ${base_ref}."
