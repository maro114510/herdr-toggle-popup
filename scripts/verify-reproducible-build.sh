#!/bin/sh
# scripts/verify-reproducible-build.sh — build the release targets twice and compare them.
#
# Usage: sh scripts/verify-reproducible-build.sh <version>
#
# Both passes build darwin/amd64, darwin/arm64, linux/amd64, and linux/arm64 with the flags
# the release workflow uses, from two copies of the source at different paths, each with its
# own empty GOCACHE. -trimpath keeps the copy path out of the binary, so equal digests show
# the output depends only on the source, the toolchain, and these flags. The toolchain is
# fixed by the toolchain directive in go.mod, which setup-go reads through go-version-file.
set -eu

RELEASE_TARGETS="darwin/amd64 darwin/arm64 linux/amd64 linux/arm64"

usage() {
  echo "usage: sh scripts/verify-reproducible-build.sh <version>" >&2
  exit 64
}

if [ "$#" -ne 1 ] || [ -z "$1" ]; then
  usage
fi

version="$1"

for tool in awk cp dirname mkdir mktemp; do
  if ! command -v "${tool}" >/dev/null 2>&1; then
    echo "verify-reproducible-build.sh: ${tool} is required on PATH" >&2
    exit 1
  fi
done

if ! command -v go >/dev/null 2>&1; then
  echo "verify-reproducible-build.sh: go is required on PATH" >&2
  exit 1
fi

if command -v sha256sum >/dev/null 2>&1; then
  sha256_command="sha256sum"
elif command -v shasum >/dev/null 2>&1; then
  sha256_command="shasum"
else
  echo "verify-reproducible-build.sh: no sha256sum or shasum found on PATH" >&2
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

sha256_of() {
  if [ "${sha256_command}" = "sha256sum" ]; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

work_dir="$(mktemp -d)"
trap 'rm -rf "${work_dir}"' EXIT

first_src="${work_dir}/one/src"
second_src="${work_dir}/two/a/deeper/path/src"
first_cache="${work_dir}/one/cache"
second_cache="${work_dir}/two/cache"

stage_source() {
  dest="$1"
  mkdir -p "${dest}"
  (cd "${ROOT_DIR}" && cp -a . "${dest}/")
}

build_targets() {
  src="$1"
  cache="$2"
  mkdir -p "${src}/dist" "${cache}"
  for target in ${RELEASE_TARGETS}; do
    goos="${target%/*}"
    goarch="${target#*/}"
    if ! (cd "${src}" && GOCACHE="${cache}" CGO_ENABLED=0 GOOS="${goos}" GOARCH="${goarch}" \
      go build -trimpath -ldflags "-s -w -X main.version=${version}" \
      -o "${src}/dist/toggle-popup_${goos}_${goarch}" .); then
      echo "verify-reproducible-build.sh: go build failed for ${goos}/${goarch}" >&2
      exit 1
    fi
  done
}

stage_source "${first_src}"
stage_source "${second_src}"
build_targets "${first_src}" "${first_cache}"
build_targets "${second_src}" "${second_cache}"

for target in ${RELEASE_TARGETS}; do
  goos="${target%/*}"
  goarch="${target#*/}"
  asset="toggle-popup_${goos}_${goarch}"
  first_digest="$(sha256_of "${first_src}/dist/${asset}")"
  second_digest="$(sha256_of "${second_src}/dist/${asset}")"
  if [ "${first_digest}" != "${second_digest}" ]; then
    echo "verify-reproducible-build.sh: digest mismatch for ${asset}" >&2
    echo "  first build:  ${first_digest}" >&2
    echo "  second build: ${second_digest}" >&2
    exit 1
  fi
  printf '%s  %s\n' "${first_digest}" "${asset}"
done

host_goos="$(go env GOOS)"
host_goarch="$(go env GOARCH)"
case " ${RELEASE_TARGETS} " in
  *" ${host_goos}/${host_goarch} "*)
    ;;
  *)
    echo "verify-reproducible-build.sh: host ${host_goos}/${host_goarch} is not one of the release targets (${RELEASE_TARGETS})" >&2
    exit 1
    ;;
esac

host_asset="toggle-popup_${host_goos}_${host_goarch}"
reported_version="$("${first_src}/dist/${host_asset}" version)"
if [ "${reported_version}" != "${version}" ]; then
  echo "verify-reproducible-build.sh: ${host_asset} reports version ${reported_version}, expected ${version}" >&2
  exit 1
fi

echo "verify-reproducible-build.sh: all four targets matched across two independent builds"
echo "verify-reproducible-build.sh: ${host_asset} reports version ${version}"
