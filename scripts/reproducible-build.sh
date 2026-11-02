#!/bin/sh
# scripts/reproducible-build.sh — build the four release targets twice and fail
# unless both independent builds produce identical binaries.
#
# The first build uses the working tree, the second an exported copy of HEAD at a
# different path, so a VCS stamp or a build path leaking into the binary shows up
# as a digest mismatch. Run it from a clean checkout: uncommitted changes make
# the two builds differ by design.
#
# Usage: sh scripts/reproducible-build.sh <version> <output-dir>
# Leaves the first build's binaries in <output-dir>.
set -eu

usage() {
  printf '%s\n' 'usage: scripts/reproducible-build.sh <version> <output-dir>' >&2
  exit 2
}

[ "$#" -eq 2 ] || usage
version="$1"
out_dir="$2"
[ -n "${version}" ] || usage
[ -n "${out_dir}" ] || usage

script_dir="$(cd "$(dirname "$0")" && pwd)"
root_dir="$(cd "${script_dir}/.." && pwd)"

case "${out_dir}" in
  /*) ;;
  *) out_dir="$(pwd)/${out_dir}" ;;
esac

targets="darwin/amd64 darwin/arm64 linux/amd64 linux/arm64"

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | cut -d ' ' -f 1
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | cut -d ' ' -f 1
  else
    printf '%s\n' 'reproducible-build.sh: no sha256sum or shasum found on PATH' >&2
    exit 1
  fi
}

build_targets() {
  source_dir="$1"
  dest_dir="$2"
  mkdir -p "${dest_dir}"
  for target in ${targets}; do
    goos="${target%/*}"
    goarch="${target#*/}"
    printf 'reproducible-build.sh: building %s\n' "${target}" >&2
    (
      cd "${source_dir}"
      CGO_ENABLED=0 GOOS="${goos}" GOARCH="${goarch}" go build \
        -trimpath \
        -buildvcs=false \
        -ldflags "-s -w -X main.version=${version}" \
        -o "${dest_dir}/toggle-popup_${goos}_${goarch}" .
    )
  done
}

work_dir="$(mktemp -d)"
trap 'rm -rf "${work_dir}"' EXIT

second_source="${work_dir}/source-tree-at-a-second-path"
second_dist="${work_dir}/dist-at-a-second-path"
mkdir -p "${second_source}"
git -C "${root_dir}" archive HEAD | tar -x -C "${second_source}"

build_targets "${root_dir}" "${out_dir}"
build_targets "${second_source}" "${second_dist}"

failed=0
for target in ${targets}; do
  goos="${target%/*}"
  goarch="${target#*/}"
  asset="toggle-popup_${goos}_${goarch}"
  first_bin="${out_dir}/${asset}"
  second_bin="${second_dist}/${asset}"

  first_sha="$(sha256_of "${first_bin}")"
  second_sha="$(sha256_of "${second_bin}")"
  if [ "${first_sha}" != "${second_sha}" ]; then
    printf 'reproducible-build.sh: digest mismatch for %s\n  working tree: %s\n  exported HEAD: %s\n' \
      "${asset}" "${first_sha}" "${second_sha}" >&2
    failed=1
  else
    printf '%s  %s\n' "${first_sha}" "${asset}"
  fi

  if ! grep -aqF -- "${version}" "${first_bin}"; then
    printf 'reproducible-build.sh: %s does not embed version %s\n' "${asset}" "${version}" >&2
    failed=1
  fi
done

# A digest alone cannot show that the version flag reached the binary, so run the
# host's own target and compare what it reports.
host_os=""
case "$(uname -s)" in
  Darwin) host_os=darwin ;;
  Linux) host_os=linux ;;
esac
host_arch=""
case "$(uname -m)" in
  x86_64) host_arch=amd64 ;;
  arm64 | aarch64) host_arch=arm64 ;;
esac
if [ -n "${host_os}" ] && [ -n "${host_arch}" ]; then
  host_bin="${out_dir}/toggle-popup_${host_os}_${host_arch}"
  if [ -x "${host_bin}" ]; then
    if ! reported="$("${host_bin}" version)"; then
      printf 'reproducible-build.sh: %s failed to report its version\n' "${host_bin}" >&2
      failed=1
    elif [ "${reported}" != "${version}" ]; then
      printf 'reproducible-build.sh: %s reports version %s, want %s\n' "${host_bin}" "${reported}" "${version}" >&2
      failed=1
    fi
  fi
fi

[ "${failed}" -eq 0 ] || {
  printf '%s\n' 'reproducible-build.sh: release builds are not reproducible' >&2
  exit 1
}
