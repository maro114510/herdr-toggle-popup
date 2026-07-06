#!/usr/bin/env bats
#
# Test list (see on-pane-closed.sh):
# - deletes the registry entry whose pane_id matches $HERDR_PANE_ID
# - leaves other entries (different pane_id) untouched
# - HERDR_PANE_ID unset: exits 0 without ever touching the registry
# - HERDR_PANE_ID empty: exits 0 without ever touching the registry
# - HERDR_PANE_ID matches nothing registered: exits 0, registry unchanged

setup() {
  ON_PANE_CLOSED_SH="$BATS_TEST_DIRNAME/../on-pane-closed.sh"
  export HERDR_PLUGIN_STATE_DIR="$BATS_TEST_TMPDIR/plugin-state"
  POPUPS_FILE="$HERDR_PLUGIN_STATE_DIR/popups.json"
  # shellcheck source=../state.sh
  source "$BATS_TEST_DIRNAME/../state.sh"
}

@test "deletes the registry entry whose pane_id matches HERDR_PANE_ID" {
  state_set "workspace:ws1:shell" "pane-1" "maro114510.toggle-popup" "shell" "workspace" "ws1" "" 1

  export HERDR_PANE_ID="pane-1"
  run bash "$ON_PANE_CLOSED_SH"
  [ "$status" -eq 0 ]

  run state_get "workspace:ws1:shell"
  [ "$status" -eq 1 ]
}

@test "leaves entries with a different pane_id untouched" {
  state_set "workspace:ws1:shell" "pane-1" "maro114510.toggle-popup" "shell" "workspace" "ws1" "" 1
  state_set "workspace:ws2:shell" "pane-2" "maro114510.toggle-popup" "shell" "workspace" "ws2" "" 2

  export HERDR_PANE_ID="pane-1"
  run bash "$ON_PANE_CLOSED_SH"
  [ "$status" -eq 0 ]

  run state_get "workspace:ws2:shell"
  [ "$status" -eq 0 ]
  [ "$(echo "$output" | jq -r .pane_id)" = "pane-2" ]
}

@test "HERDR_PANE_ID unset: exits 0 without ever touching the registry" {
  state_set "workspace:ws1:shell" "pane-1" "maro114510.toggle-popup" "shell" "workspace" "ws1" "" 1
  before="$(cat "$POPUPS_FILE")"

  unset HERDR_PANE_ID
  run bash "$ON_PANE_CLOSED_SH"
  [ "$status" -eq 0 ]

  [ "$(cat "$POPUPS_FILE")" = "$before" ]
}

@test "HERDR_PANE_ID empty: exits 0 without ever touching the registry" {
  [ ! -e "$POPUPS_FILE" ]

  export HERDR_PANE_ID=""
  run bash "$ON_PANE_CLOSED_SH"
  [ "$status" -eq 0 ]

  [ ! -e "$POPUPS_FILE" ]
}

@test "HERDR_PANE_ID matches nothing registered: exits 0, registry unchanged" {
  state_set "workspace:ws1:shell" "pane-1" "maro114510.toggle-popup" "shell" "workspace" "ws1" "" 1

  export HERDR_PANE_ID="pane-unrelated"
  run bash "$ON_PANE_CLOSED_SH"
  [ "$status" -eq 0 ]

  run state_get "workspace:ws1:shell"
  [ "$status" -eq 0 ]
  [ "$(echo "$output" | jq -r .pane_id)" = "pane-1" ]
}
