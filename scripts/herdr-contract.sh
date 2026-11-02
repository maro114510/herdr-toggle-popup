#!/bin/sh
# scripts/herdr-contract.sh — acquire one Herdr release track and run the plugin contract.
# The contract is TestHerdrContract; this script resolves the release, verifies that the
# downloaded binary is the requested one, records it, and hands it to the test.
set -eu

usage() {
  echo "usage: $0 <minimum|stable|preview>" >&2
}

if [ "$#" -ne 1 ]; then
  echo "herdr-contract.sh: expected one track argument" >&2
  usage
  exit 2
fi

track=$1
case "$track" in
  minimum | stable | preview) ;;
  *)
    echo "herdr-contract.sh: unknown track: $track" >&2
    usage
    exit 2
    ;;
esac

script_dir=$(cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(cd -- "${script_dir}/.." && pwd)
manifest="${repo_root}/herdr-plugin.toml"
herdr_repo="herdrdev/herdr"

if ! command -v tmux >/dev/null 2>&1; then
  echo "herdr-contract.sh: tmux is required but was not found on PATH" >&2
  exit 1
fi
tmux_version=$(tmux -V)
tmux_version=${tmux_version#tmux }

work_root=${HERDR_CONTRACT_WORK_DIR:-}
created_work=0
if [ -z "$work_root" ]; then
  work_root=$(mktemp -d "${TMPDIR:-/tmp}/herdr-contract.XXXXXX")
  created_work=1
fi
mkdir -p "$work_root"
log_dir=${HERDR_CONTRACT_LOG_DIR:-"${work_root}/logs"}
mkdir -p "$log_dir"

cleanup() {
  status=$?
  trap - EXIT
  if [ "$status" -eq 0 ] && [ "$created_work" -eq 1 ]; then
    rm -rf "$work_root"
  fi
}
trap cleanup EXIT

# resolve_tag names the release under test. The minimum follows the manifest floor so raising it
# updates CI in the same commit; stable and preview are read from GitHub on every run.
resolve_tag() {
  case "$track" in
    minimum)
      minimum=$(sed -nE 's/^[[:space:]]*min_herdr_version[[:space:]]*=[[:space:]]*"([^"]+)".*/\1/p' "$manifest" | head -n 1)
      if [ -z "$minimum" ]; then
        echo "herdr-contract.sh: could not read min_herdr_version from ${manifest}" >&2
        exit 1
      fi
      printf 'v%s\n' "$minimum"
      ;;
    stable)
      gh api "repos/${herdr_repo}/releases/latest" --jq '.tag_name'
      ;;
    preview)
      gh api "repos/${herdr_repo}/releases?per_page=30" \
        --jq '[.[] | select(.prerelease and (.draft | not))][0].tag_name'
      ;;
  esac
}

tag=$(resolve_tag)
case "$tag" in
  v[0-9]* | preview-*) ;;
  *)
    echo "herdr-contract.sh: could not resolve a ${track} Herdr release (got '${tag}')" >&2
    exit 1
    ;;
esac

revision=$(gh api "repos/${herdr_repo}/commits/${tag}" --jq '.sha')
case "$revision" in
  '' | *[!0-9a-f]*)
    echo "herdr-contract.sh: could not resolve the ${tag} commit (got '${revision}')" >&2
    exit 1
    ;;
esac
if [ "${#revision}" -ne 40 ]; then
  echo "herdr-contract.sh: unexpected ${tag} commit '${revision}'" >&2
  exit 1
fi

os_name=$(uname -s)
arch_name=$(uname -m)
case "${os_name}:${arch_name}" in
  Linux:x86_64) asset=herdr-linux-x86_64 ;;
  Linux:aarch64 | Linux:arm64) asset=herdr-linux-aarch64 ;;
  Darwin:arm64) asset=herdr-macos-aarch64 ;;
  Darwin:x86_64) asset=herdr-macos-x86_64 ;;
  *)
    echo "herdr-contract.sh: unsupported platform ${os_name} ${arch_name}" >&2
    exit 1
    ;;
esac

bin_dir="${work_root}/bin"
mkdir -p "$bin_dir"
herdr_bin="${bin_dir}/herdr"
url="https://github.com/${herdr_repo}/releases/download/${tag}/${asset}"
echo "herdr-contract.sh: downloading ${url}"
if ! curl -fsSL --retry 3 --retry-delay 2 -o "$herdr_bin" "$url"; then
  echo "herdr-contract.sh: failed to download ${asset} for ${tag}" >&2
  exit 1
fi
chmod +x "$herdr_bin"

# A release asset is not proof that the requested build ran: an older herdr on PATH, a cached
# download, or a moved tag would otherwise pass. The stable version must match the tag, and a
# preview must carry the commit its tag names.
version=$("$herdr_bin" --version)
case "$track" in
  preview)
    revision_prefix=$(printf '%s' "$revision" | cut -c1-12)
    case "$version" in
      *"$revision_prefix"*) ;;
      *)
        echo "herdr-contract.sh: expected the preview build to carry revision ${revision_prefix}; got ${version}" >&2
        exit 1
        ;;
    esac
    ;;
  *)
    expected="herdr ${tag#v}"
    if [ "$version" != "$expected" ]; then
      echo "herdr-contract.sh: expected ${expected}; got ${version}" >&2
      exit 1
    fi
    ;;
esac

record="track=${track} version=${version#herdr } tag=${tag} revision=${revision} tmux=${tmux_version}"
printf '%s\n' "$record"
printf '%s\n' "$record" >"${log_dir}/record.txt"
if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
  gate=required
  if [ "$track" = "preview" ]; then
    gate=advisory
  fi
  {
    printf '### Herdr contract (%s)\n\n' "$track"
    printf '```\n'
    printf '%s\n' "$record" "gate=$gate"
    printf '```\n'
    if [ "$gate" = "advisory" ]; then
      printf '\nA preview failure is an early warning and does not change release eligibility.\n'
    fi
  } >>"$GITHUB_STEP_SUMMARY"
fi

cd "$repo_root"
export HERDR_CONTRACT_BIN="$herdr_bin"
export HERDR_CONTRACT_TRACK="$track"
export HERDR_CONTRACT_REQUIRE=1
export HERDR_CONTRACT_LOG_DIR="$log_dir"
go test -count=1 -run '^TestHerdrContract$' -v .

echo "herdr-contract.sh: ${track} contract passed"
