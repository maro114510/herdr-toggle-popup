#!/bin/sh
# scripts/publish-release.sh — publish a draft GitHub release once every required asset is attached.
#
# Usage: sh scripts/publish-release.sh <tag>
#
# Publication freezes the assets of an immutable release, so this is the last step of the release
# workflow: it refuses a release that is not a draft or lacks any required asset, publishes the
# draft, and then reads the release back to confirm GitHub froze it. Run it with GH_TOKEN and
# GH_REPO set, as the release workflow does.
set -eu

usage() {
  echo "usage: sh scripts/publish-release.sh <tag>" >&2
  exit 64
}

if [ "$#" -ne 1 ]; then
  usage
fi

tag="$1"

if ! command -v gh >/dev/null 2>&1; then
  echo "publish-release.sh: gh is required on PATH" >&2
  exit 1
fi

if ! is_draft="$(gh release view "${tag}" --json isDraft --jq .isDraft)"; then
  echo "publish-release.sh: cannot read release ${tag}" >&2
  exit 1
fi
if [ "${is_draft}" != "true" ]; then
  echo "publish-release.sh: release ${tag} is not a draft" >&2
  exit 1
fi

if ! assets="$(gh release view "${tag}" --json assets --jq '.assets[].name')"; then
  echo "publish-release.sh: cannot read release ${tag}" >&2
  exit 1
fi

for asset in \
  toggle-popup_darwin_amd64 \
  toggle-popup_darwin_arm64 \
  toggle-popup_linux_amd64 \
  toggle-popup_linux_arm64 \
  sbom_toggle-popup_darwin_amd64.cdx.json \
  sbom_toggle-popup_darwin_arm64.cdx.json \
  sbom_toggle-popup_linux_amd64.cdx.json \
  sbom_toggle-popup_linux_arm64.cdx.json \
  checksums.txt; do
  case "
${assets}
" in
    *"
${asset}
"*) ;;
    *)
      echo "publish-release.sh: release ${tag} is missing asset ${asset}; nothing was published" >&2
      exit 1
      ;;
  esac
done

if ! gh release edit "${tag}" --draft=false; then
  echo "publish-release.sh: cannot publish release ${tag}" >&2
  exit 1
fi

if ! immutable="$(gh release view "${tag}" --json isImmutable --jq .isImmutable)"; then
  echo "publish-release.sh: cannot read release ${tag} after publication" >&2
  exit 1
fi
if [ "${immutable}" != "true" ]; then
  echo "publish-release.sh: release ${tag} is not immutable; enable immutable releases in the repository settings" >&2
  exit 1
fi

echo "publish-release.sh: published immutable release ${tag}"
