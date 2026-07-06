#!/usr/bin/env bats

setup() {
  MANIFEST="$BATS_TEST_DIRNAME/../herdr-plugin.toml"
  KEYBINDINGS="$BATS_TEST_DIRNAME/../keybindings.toml"
}

block() { # $1=file $2=table header, e.g. "[[actions]]"
  awk -v hdr="$2" '
    $0 == hdr { found=1; next }
    found && /^\[\[/ { exit }
    found { print }
  ' "$1"
}

@test "manifest declares plugin id, version and min_herdr_version" {
  grep -qE '^id = "maro114510\.toggle-popup"$' "$MANIFEST"
  grep -qE '^min_herdr_version = "0\.7\.0"$' "$MANIFEST"
}

@test "manifest declares supported platforms" {
  grep -qE '^platforms = \["macos", "linux"\]$' "$MANIFEST"
}

@test "manifest declares the toggle-shell action" {
  run block "$MANIFEST" "[[actions]]"
  [ "$status" -eq 0 ]
  echo "$output" | grep -qE '^id = "toggle-shell"$'
  echo "$output" | grep -qE '^title = "Toggle popup shell"$'
  echo "$output" | grep -qE '^contexts = \["workspace", "tab", "pane"\]$'
  echo "$output" | grep -qE '^command = \["bash", "toggle\.sh", "shell"\]$'
}

@test "manifest declares the shell pane" {
  run block "$MANIFEST" "[[panes]]"
  [ "$status" -eq 0 ]
  echo "$output" | grep -qE '^id = "shell"$'
  echo "$output" | grep -qE '^title = "Popup Shell"$'
  echo "$output" | grep -qE '^placement = "overlay"$'
  echo "$output" | grep -qE '^command = \["bash", "popup-shell\.sh"\]$'
}

@test "manifest declares the pane.closed event hook" {
  run block "$MANIFEST" "[[events]]"
  [ "$status" -eq 0 ]
  echo "$output" | grep -qE '^on = "pane\.closed"$'
  echo "$output" | grep -qE '^command = \["bash", "on-pane-closed\.sh"\]$'
}

@test "keybindings.toml declares the alt+l plugin_action binding" {
  run block "$KEYBINDINGS" "[[keys.command]]"
  [ "$status" -eq 0 ]
  echo "$output" | grep -qE '^key = "alt\+l"$'
  echo "$output" | grep -qE '^type = "plugin_action"$'
  echo "$output" | grep -qE '^command = "maro114510\.toggle-popup\.toggle-shell"$'
}
