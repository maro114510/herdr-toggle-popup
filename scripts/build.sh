#!/bin/sh
# scripts/build.sh — herdr [[build]] step.
# Builds from source when a Go toolchain is available, otherwise downloads a
# checksum-verified prebuilt binary from the GitHub Release matching the
# version pinned in herdr-plugin.toml. Either path produces ./bin/toggle-popup.
set -eu

REPO="maro114510/herdr-toggle-popup"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
MANIFEST="${ROOT_DIR}/herdr-plugin.toml"
BIN_DIR="${ROOT_DIR}/bin"
BIN_PATH="${BIN_DIR}/toggle-popup"

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    echo "build.sh: no sha256sum or shasum found on PATH" >&2
    exit 1
  fi
}

ensure_tmux() {
  if command -v tmux >/dev/null 2>&1; then
    echo "build.sh: tmux found on PATH"
    return
  fi

  echo "build.sh: tmux not found on PATH; attempting to install it"

  if command -v brew >/dev/null 2>&1; then
    brew install tmux
  elif command -v apt-get >/dev/null 2>&1 && [ "$(id -u)" -eq 0 ]; then
    apt-get update
    apt-get install -y tmux
  elif command -v dnf >/dev/null 2>&1 && [ "$(id -u)" -eq 0 ]; then
    dnf install -y tmux
  elif command -v pacman >/dev/null 2>&1 && [ "$(id -u)" -eq 0 ]; then
    pacman -Sy --noconfirm tmux
  else
    cat >&2 <<'EOF'
build.sh: tmux is required but could not be installed automatically.
Install tmux, then rerun this build:
  macOS:  brew install tmux
  Debian/Ubuntu:  sudo apt-get install tmux
  Fedora/RHEL:  sudo dnf install tmux
  Arch:  sudo pacman -S tmux
EOF
    exit 1
  fi

  if ! command -v tmux >/dev/null 2>&1; then
    echo "build.sh: tmux installation finished but tmux is still not on PATH" >&2
    exit 1
  fi
  echo "build.sh: tmux installed"
}

ensure_tmux

if command -v go >/dev/null 2>&1; then
  echo "build.sh: go found on PATH, building from source"
  mkdir -p "${BIN_DIR}"
  (cd "${ROOT_DIR}" && CGO_ENABLED=0 go build -trimpath -o "${BIN_PATH}" .)
  exit 0
fi

echo "build.sh: go not found on PATH, downloading a prebuilt binary"

os_name="$(uname -s)"
case "${os_name}" in
  Darwin) os="darwin" ;;
  Linux) os="linux" ;;
  *)
    echo "build.sh: unsupported OS '${os_name}' (supported: Darwin, Linux)" >&2
    exit 1
    ;;
esac

arch_name="$(uname -m)"
case "${arch_name}" in
  x86_64) arch="amd64" ;;
  arm64 | aarch64) arch="arm64" ;;
  *)
    echo "build.sh: unsupported architecture '${arch_name}' (supported: x86_64, arm64, aarch64)" >&2
    exit 1
    ;;
esac

version="$(sed -nE 's/^[[:space:]]*version[[:space:]]*=[[:space:]]*"([^"]+)".*/\1/p' "${MANIFEST}" | head -n 1)"
if [ -z "${version}" ]; then
  echo "build.sh: could not read version from ${MANIFEST}" >&2
  exit 1
fi

tag="v${version}"
asset_name="toggle-popup_${os}_${arch}"
base_url="https://github.com/${REPO}/releases/download/${tag}"

work_dir="$(mktemp -d)"
trap 'rm -rf "${work_dir}"' EXIT

echo "build.sh: downloading ${asset_name} from release ${tag}"
if ! curl -fsSL --connect-timeout 10 --max-time 120 -o "${work_dir}/${asset_name}" "${base_url}/${asset_name}"; then
  echo "build.sh: failed to download ${asset_name} from release ${tag}" >&2
  exit 1
fi

if ! curl -fsSL --connect-timeout 10 --max-time 60 -o "${work_dir}/checksums.txt" "${base_url}/checksums.txt"; then
  echo "build.sh: failed to download checksums.txt from release ${tag}" >&2
  exit 1
fi

expected="$(awk -v f="${asset_name}" '$2 == f { print $1 }' "${work_dir}/checksums.txt")"
if [ -z "${expected}" ]; then
  echo "build.sh: no checksum entry for ${asset_name} in checksums.txt" >&2
  exit 1
fi

actual="$(cd "${work_dir}" && sha256_of "${asset_name}")"
if [ "${expected}" != "${actual}" ]; then
  echo "build.sh: checksum mismatch for ${asset_name} (expected ${expected}, got ${actual})" >&2
  exit 1
fi

mkdir -p "${BIN_DIR}"
mv "${work_dir}/${asset_name}" "${BIN_PATH}"
chmod +x "${BIN_PATH}"
echo "build.sh: installed ${BIN_PATH}"
