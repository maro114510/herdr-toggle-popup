#!/usr/bin/env bats
#
# Test list (see context.sh):
# - returns a field's value when present
# - returns empty string when the field is absent
# - returns empty string when HERDR_PLUGIN_CONTEXT_JSON is unset
# - returns empty string when HERDR_PLUGIN_CONTEXT_JSON is malformed JSON
# - reads distinct fields independently out of the same JSON (present and absent cases)

setup() {
  CONTEXT_SH="$BATS_TEST_DIRNAME/../context.sh"
  # shellcheck source=/dev/null
  source "$CONTEXT_SH"
}

@test "returns a field's value when present" {
  export HERDR_PLUGIN_CONTEXT_JSON='{"focused_pane_cwd":"/focused/cwd"}'
  run context_field focused_pane_cwd
  [ "$status" -eq 0 ]
  [ "$output" = "/focused/cwd" ]
}

@test "returns empty string when the field is absent" {
  export HERDR_PLUGIN_CONTEXT_JSON='{"workspace_id":"ws1"}'
  run context_field focused_pane_cwd
  [ "$status" -eq 0 ]
  [ "$output" = "" ]
}

@test "returns empty string when HERDR_PLUGIN_CONTEXT_JSON is unset" {
  unset HERDR_PLUGIN_CONTEXT_JSON
  run context_field focused_pane_cwd
  [ "$status" -eq 0 ]
  [ "$output" = "" ]
}

@test "returns empty string when HERDR_PLUGIN_CONTEXT_JSON is malformed JSON" {
  export HERDR_PLUGIN_CONTEXT_JSON='not-json'
  run context_field focused_pane_cwd
  [ "$status" -eq 0 ]
  [ "$output" = "" ]
}

@test "reads distinct fields independently out of the same JSON" {
  export HERDR_PLUGIN_CONTEXT_JSON='{"workspace_id":"ws1","focused_pane_id":"ws1:p1","focused_pane_cwd":"/focused/cwd"}'
  [ "$(context_field workspace_id)" = "ws1" ]
  [ "$(context_field focused_pane_id)" = "ws1:p1" ]
  [ "$(context_field focused_pane_cwd)" = "/focused/cwd" ]
  [ "$(context_field tab_id)" = "" ]
}
