#!/bin/sh
# scripts/generate-sbom.sh — write and validate one SBOM per release binary.
#
# Usage: sh scripts/generate-sbom.sh <dist-dir> <version>
#
# Both tools come from the release workflow, pinned there by version and SHA-256:
#   syft       reads the Go build information of each binary, so the SBOM lists the
#              modules the distributed binary actually contains, not what go.mod requests
#   cyclonedx  validates every document, so an unreadable SBOM fails the release instead
#              of shipping
set -eu

usage() {
  echo "usage: sh scripts/generate-sbom.sh <dist-dir> <version>" >&2
  exit 64
}

if [ "$#" -ne 2 ]; then
  usage
fi

dist_dir="$1"
version="$2"

for tool in syft cyclonedx; do
  if ! command -v "${tool}" >/dev/null 2>&1; then
    echo "generate-sbom.sh: ${tool} is required on PATH; the release workflow installs the pinned version" >&2
    exit 1
  fi
done

found=0
for binary in "${dist_dir}"/toggle-popup_*; do
  if [ ! -f "${binary}" ]; then
    continue
  fi
  found=1
  asset="${binary##*/}"
  sbom="${dist_dir}/sbom_${asset}.cdx.json"
  echo "generate-sbom.sh: writing ${sbom}"
  if ! syft scan "file:${binary}" \
    --source-name "${asset}" \
    --source-version "${version}" \
    --output "cyclonedx-json@1.6=${sbom}"; then
    echo "generate-sbom.sh: SBOM generation failed for ${asset}" >&2
    exit 1
  fi
  if ! cyclonedx validate \
    --input-file "${sbom}" \
    --input-format json \
    --input-version 1.6 \
    --fail-on-errors; then
    echo "generate-sbom.sh: SBOM validation failed for ${sbom}" >&2
    exit 1
  fi
done

if [ "${found}" -eq 0 ]; then
  echo "generate-sbom.sh: no release binaries found in ${dist_dir}" >&2
  exit 1
fi
