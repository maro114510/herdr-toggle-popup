#!/usr/bin/env bats

setup() {
  STATE_SH="$BATS_TEST_DIRNAME/../state.sh"
  export HERDR_PLUGIN_STATE_DIR="$BATS_TEST_TMPDIR/plugin-state"
  POPUPS_FILE="$HERDR_PLUGIN_STATE_DIR/popups.json"
  # shellcheck source=/dev/null
  source "$STATE_SH"
}

@test "state_read returns an empty registry when popups.json does not exist" {
  run state_read
  [ "$status" -eq 0 ]
  [ "$output" = '{"version":1,"popups":{}}' ]
  [ ! -e "$POPUPS_FILE" ]
}

@test "state_set then state_get round-trips an entry" {
  state_set "workspace:ws1:shell" "pane-1" "maro114510.toggle-popup" "shell" "workspace" "ws1" "" 1720000000000

  run state_get "workspace:ws1:shell"
  [ "$status" -eq 0 ]
  [ "$(echo "$output" | jq -r .pane_id)" = "pane-1" ]
  [ "$(echo "$output" | jq -r .plugin_id)" = "maro114510.toggle-popup" ]
  [ "$(echo "$output" | jq -r .entrypoint)" = "shell" ]
  [ "$(echo "$output" | jq -r .scope)" = "workspace" ]
  [ "$(echo "$output" | jq -r .workspace_id)" = "ws1" ]
  [ "$(echo "$output" | jq -r .tab_id)" = "null" ]
  [ "$(echo "$output" | jq -r .created_at_unix_ms)" = "1720000000000" ]
}

@test "state_get fails for a key that was never set" {
  run state_get "workspace:missing:shell"
  [ "$status" -eq 1 ]
  [ -z "$output" ]
}

@test "state_set preserves other existing entries" {
  state_set "workspace:ws1:shell" "pane-1" "maro114510.toggle-popup" "shell" "workspace" "ws1" "" 1
  state_set "workspace:ws2:shell" "pane-2" "maro114510.toggle-popup" "shell" "workspace" "ws2" "" 2

  run state_get "workspace:ws1:shell"
  [ "$(echo "$output" | jq -r .pane_id)" = "pane-1" ]

  run state_get "workspace:ws2:shell"
  [ "$(echo "$output" | jq -r .pane_id)" = "pane-2" ]
}

@test "state_delete removes an entry so state_get subsequently fails" {
  state_set "workspace:ws1:shell" "pane-1" "maro114510.toggle-popup" "shell" "workspace" "ws1" "" 1
  state_delete "workspace:ws1:shell"

  run state_get "workspace:ws1:shell"
  [ "$status" -eq 1 ]
}

@test "state_delete on a nonexistent key does not error" {
  run state_delete "workspace:missing:shell"
  [ "$status" -eq 0 ]
}

@test "the registry written to disk matches the documented schema" {
  state_set "workspace:ws1:shell" "pane-1" "maro114510.toggle-popup" "shell" "workspace" "ws1" "" 1

  run jq -e '
    .version == 1
    and (.popups | type) == "object"
    and (.popups["workspace:ws1:shell"] | keys | sort) ==
      ["created_at_unix_ms", "entrypoint", "pane_id", "plugin_id", "scope", "tab_id", "workspace_id"]
  ' "$POPUPS_FILE"
  [ "$status" -eq 0 ]
}

@test "state_set creates the state directory when missing" {
  [ ! -d "$HERDR_PLUGIN_STATE_DIR" ]
  state_set "workspace:ws1:shell" "pane-1" "maro114510.toggle-popup" "shell" "workspace" "ws1" "" 1
  [ -f "$POPUPS_FILE" ]
}

@test "an interrupted write never leaves the target path observed as a partial file" {
  mkdir -p "$HERDR_PLUGIN_STATE_DIR"
  old='{"version":1,"popups":{"pre-existing":{"pane_id":"old"}}}'
  printf '%s' "$old" > "$POPUPS_FILE"

  observe="$BATS_TEST_TMPDIR/observe"
  mkdir -p "$observe"
  stub_dir="$BATS_TEST_TMPDIR/bin"
  mkdir -p "$stub_dir"
  cat > "$stub_dir/mv" <<STUB
#!/usr/bin/env bash
src="\$1"
dest="\$2"
if [ -f "\$dest" ]; then
  cp "\$dest" "$observe/target_before_rename"
fi
cp "\$src" "$observe/temp_before_rename"
/bin/mv "\$src" "\$dest"
STUB
  chmod +x "$stub_dir/mv"

  new='{"version":1,"popups":{"fresh":{"pane_id":"new"}}}'
  PATH="$stub_dir:$PATH" state_write_registry "$new"

  [ "$(cat "$observe/target_before_rename")" = "$old" ]
  [ "$(cat "$observe/temp_before_rename")" = "$new" ]
  [ "$(cat "$POPUPS_FILE")" = "$new" ]
}

@test "a corrupted popups.json is backed up and read returns a fresh empty registry" {
  mkdir -p "$HERDR_PLUGIN_STATE_DIR"
  printf '%s' 'not valid json{{{' > "$POPUPS_FILE"

  run state_read
  [ "$status" -eq 0 ]
  [ "$output" = '{"version":1,"popups":{}}' ]

  run bash -c 'ls "'"$HERDR_PLUGIN_STATE_DIR"'"/popups.json.bak.*'
  [ "$status" -eq 0 ]

  [ "$(cat "$POPUPS_FILE")" = '{"version":1,"popups":{}}' ]
}

@test "a popups.json missing required schema fields is treated as corrupt" {
  mkdir -p "$HERDR_PLUGIN_STATE_DIR"
  printf '%s' '{"foo":"bar"}' > "$POPUPS_FILE"

  run state_read
  [ "$status" -eq 0 ]
  [ "$output" = '{"version":1,"popups":{}}' ]
}

@test "state_delete_by_pane_id removes the entry with a matching pane_id" {
  state_set "workspace:ws1:shell" "pane-1" "maro114510.toggle-popup" "shell" "workspace" "ws1" "" 1
  state_delete_by_pane_id "pane-1"

  run state_get "workspace:ws1:shell"
  [ "$status" -eq 1 ]
}

@test "state_delete_by_pane_id leaves entries with a different pane_id untouched" {
  state_set "workspace:ws1:shell" "pane-1" "maro114510.toggle-popup" "shell" "workspace" "ws1" "" 1
  state_set "workspace:ws2:shell" "pane-2" "maro114510.toggle-popup" "shell" "workspace" "ws2" "" 2
  state_delete_by_pane_id "pane-1"

  run state_get "workspace:ws2:shell"
  [ "$status" -eq 0 ]
  [ "$(echo "$output" | jq -r .pane_id)" = "pane-2" ]
}

@test "state_delete_by_pane_id on a pane_id that matches nothing does not error" {
  state_set "workspace:ws1:shell" "pane-1" "maro114510.toggle-popup" "shell" "workspace" "ws1" "" 1
  run state_delete_by_pane_id "pane-missing"
  [ "$status" -eq 0 ]

  run state_get "workspace:ws1:shell"
  [ "$status" -eq 0 ]
}

@test "state_delete_by_pane_id on an empty registry does not error" {
  run state_delete_by_pane_id "pane-1"
  [ "$status" -eq 0 ]
}
