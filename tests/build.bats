#!/usr/bin/env bats
#
# Test list:
# - builds from source via `go build` when go is on PATH
# - downloads, checksum-verifies, and installs the matching release asset when go is absent
# - rejects a corrupted download (checksum mismatch) without installing a binary
# - rejects an unsupported OS
# - rejects an unsupported architecture

setup() {
  PROJECT_DIR="$BATS_TEST_TMPDIR/project"
  mkdir -p "$PROJECT_DIR/scripts"
  cp "$BATS_TEST_DIRNAME/../scripts/build.sh" "$PROJECT_DIR/scripts/build.sh"
  printf 'version = "9.9.9"\n' >"$PROJECT_DIR/herdr-plugin.toml"

  # An isolated PATH containing only an explicit allowlist of real external
  # commands build.sh depends on, so "go absent" is deterministic regardless
  # of whether the host running these tests actually has Go installed.
  ISOLATED_BIN="$BATS_TEST_TMPDIR/isolated-bin"
  mkdir -p "$ISOLATED_BIN"
  for cmd in sh dirname mkdir mv chmod sed head awk mktemp rm sha256sum shasum cp cat; do
    real_path="$(command -v "$cmd" 2>/dev/null || true)"
    [ -n "$real_path" ] && ln -sf "$real_path" "$ISOLATED_BIN/$cmd"
  done
}

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

fake_uname() {
  cat >"$ISOLATED_BIN/uname" <<STUB
#!/bin/sh
case "\$1" in
  -s) printf '%s\n' '$1' ;;
  -m) printf '%s\n' '$2' ;;
esac
STUB
  chmod +x "$ISOLATED_BIN/uname"
}

# Fake curl that serves a fixed asset payload and a checksums.txt fixture
# instead of hitting the network. Reads -o <path> and the trailing URL.
fake_curl() {
  asset_content="$1"
  checksums_file="$2"
  cat >"$ISOLATED_BIN/curl" <<STUB
#!/bin/sh
out=""
url=""
prev=""
for a in "\$@"; do
  if [ "\$prev" = "-o" ]; then out="\$a"; fi
  case "\$a" in http*) url="\$a" ;; esac
  prev="\$a"
done
case "\$url" in
  */checksums.txt) cp "$checksums_file" "\$out" ;;
  *) printf '%s' "$asset_content" > "\$out" ;;
esac
STUB
  chmod +x "$ISOLATED_BIN/curl"
}

@test "builds from source via go build when go is on PATH" {
  go_log="$BATS_TEST_TMPDIR/go.log"
  cat >"$ISOLATED_BIN/go" <<STUB
#!/bin/sh
printf 'CGO_ENABLED=%s\n' "\$CGO_ENABLED" >"$go_log"
echo "\$@" >>"$go_log"
prev=""
out=""
for a in "\$@"; do
  if [ "\$prev" = "-o" ]; then out="\$a"; fi
  prev="\$a"
done
printf '#!/bin/sh\n' >"\$out"
chmod +x "\$out"
STUB
  chmod +x "$ISOLATED_BIN/go"

  PATH="$ISOLATED_BIN" run sh -c "cd '$PROJECT_DIR' && sh scripts/build.sh"

  [ "$status" -eq 0 ]
  [ -x "$PROJECT_DIR/bin/toggle-popup" ]
  grep -qE '^CGO_ENABLED=0$' "$go_log"
  grep -qE '(^| )-trimpath( |$)' "$go_log"
  grep -qE '(^| )-o .*/bin/toggle-popup( |$)' "$go_log"
}

@test "downloads, checksum-verifies, and installs the matching release asset when go is absent" {
  fake_uname Linux x86_64
  asset_content="fake-binary-content"
  asset_file="$BATS_TEST_TMPDIR/asset"
  printf '%s' "$asset_content" >"$asset_file"
  hash="$(sha256_of "$asset_file")"
  checksums_file="$BATS_TEST_TMPDIR/checksums.txt"
  printf '%s  toggle-popup_linux_amd64\n' "$hash" >"$checksums_file"
  fake_curl "$asset_content" "$checksums_file"

  PATH="$ISOLATED_BIN" run sh -c "cd '$PROJECT_DIR' && sh scripts/build.sh"

  [ "$status" -eq 0 ]
  [ -x "$PROJECT_DIR/bin/toggle-popup" ]
  [ "$(cat "$PROJECT_DIR/bin/toggle-popup")" = "$asset_content" ]
}

@test "rejects a corrupted download without installing a binary" {
  fake_uname Linux x86_64
  asset_content="fake-binary-content"
  checksums_file="$BATS_TEST_TMPDIR/checksums.txt"
  printf '%s  toggle-popup_linux_amd64\n' "0000000000000000000000000000000000000000000000000000000000000000" >"$checksums_file"
  fake_curl "$asset_content" "$checksums_file"

  PATH="$ISOLATED_BIN" run sh -c "cd '$PROJECT_DIR' && sh scripts/build.sh"

  [ "$status" -ne 0 ]
  [ ! -e "$PROJECT_DIR/bin/toggle-popup" ]
}

@test "rejects an unsupported OS" {
  fake_uname Plan9 x86_64

  PATH="$ISOLATED_BIN" run sh -c "cd '$PROJECT_DIR' && sh scripts/build.sh"

  [ "$status" -ne 0 ]
  [[ "$output" == *"unsupported OS"* ]]
  [ ! -e "$PROJECT_DIR/bin/toggle-popup" ]
}

@test "rejects an unsupported architecture" {
  fake_uname Linux mips

  PATH="$ISOLATED_BIN" run sh -c "cd '$PROJECT_DIR' && sh scripts/build.sh"

  [ "$status" -ne 0 ]
  [[ "$output" == *"unsupported architecture"* ]]
  [ ! -e "$PROJECT_DIR/bin/toggle-popup" ]
}
