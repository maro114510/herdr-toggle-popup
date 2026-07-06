#!/usr/bin/env bats
#
# Test list (see toggle.sh):
# - open-when-empty: opens a popup and registers its pane_id under workspace:<id>:<entrypoint>
# - open call never passes --workspace/--target-pane (overlay panes target the active pane;
#   herdr rejects those flags for overlay placement)
# - close-when-open: closes a registered popup and clears the registry entry
# - stale-pane-id-recovery: a close failure clears the stale entry and opens a fresh popup
# - open failure: prints to stderr, does not touch the registry, exits non-zero
# - missing focused-pane cwd: fails before ever invoking `herdr plugin pane open`
# - HERDR_BIN_PATH fallback: falls back to a `herdr` found on PATH when the env var is unset

setup() {
  TOGGLE_SH="$BATS_TEST_DIRNAME/../toggle.sh"
  export HERDR_PLUGIN_STATE_DIR="$BATS_TEST_TMPDIR/plugin-state"
  export HERDR_WORKSPACE_ID="ws1"
  export HERDR_PLUGIN_CONTEXT_JSON='{"workspace_id":"ws1","focused_pane_id":"ws1:p1","focused_pane_cwd":"/focused/cwd"}'
  POPUPS_FILE="$HERDR_PLUGIN_STATE_DIR/popups.json"
  # shellcheck source=../state.sh
  source "$BATS_TEST_DIRNAME/../state.sh"

  STUB_DIR="$BATS_TEST_TMPDIR/bin"
  mkdir -p "$STUB_DIR"
  export STUB_HERDR_LOG="$BATS_TEST_TMPDIR/herdr-calls.log"
  : > "$STUB_HERDR_LOG"

  cat > "$STUB_DIR/herdr" <<'STUB'
#!/usr/bin/env bash
: "${STUB_HERDR_LOG:?STUB_HERDR_LOG must be set}"
printf '%s\n' "$*" >> "$STUB_HERDR_LOG"

case "$1 $2 $3" in
  "plugin pane open")
    exit_code="${STUB_HERDR_OPEN_EXIT:-0}"
    if [ "$exit_code" -eq 0 ]; then
      pane_id="${STUB_HERDR_OPEN_PANE_ID:-new-pane-1}"
      printf '{"id":"cli:plugin","result":{"plugin_pane":{"entrypoint":"shell","pane":{"pane_id":"%s","workspace_id":"ws1"},"plugin_id":"maro114510.toggle-popup"},"type":"plugin_pane_opened"}}\n' "$pane_id"
    else
      printf '{"error":{"code":"invalid_params","message":"stub open failure"},"id":"cli:plugin"}\n' >&2
    fi
    exit "$exit_code"
    ;;
  "plugin pane close")
    exit_code="${STUB_HERDR_CLOSE_EXIT:-0}"
    if [ "$exit_code" -eq 0 ]; then
      printf '{"id":"cli:plugin","result":{"type":"plugin_pane_closed"}}\n'
    else
      printf '{"error":{"code":"plugin_pane_not_found","message":"plugin pane not found"},"id":"cli:plugin"}\n' >&2
    fi
    exit "$exit_code"
    ;;
  *)
    printf 'stub herdr: unhandled args: %s\n' "$*" >&2
    exit 99
    ;;
esac
STUB
  chmod +x "$STUB_DIR/herdr"
  export HERDR_BIN_PATH="$STUB_DIR/herdr"
}

@test "opens a new popup when nothing is registered and saves its pane_id" {
  export STUB_HERDR_OPEN_PANE_ID="pane-42"

  run bash "$TOGGLE_SH" shell
  [ "$status" -eq 0 ]

  run state_get "workspace:ws1:shell"
  [ "$status" -eq 0 ]
  [ "$(echo "$output" | jq -r .pane_id)" = "pane-42" ]
  [ "$(echo "$output" | jq -r .plugin_id)" = "maro114510.toggle-popup" ]
  [ "$(echo "$output" | jq -r .entrypoint)" = "shell" ]
  [ "$(echo "$output" | jq -r .scope)" = "workspace" ]
  [ "$(echo "$output" | jq -r .workspace_id)" = "ws1" ]

  ! grep -q "plugin pane close" "$STUB_HERDR_LOG"
}

@test "the open call passes plugin, entrypoint, placement, cwd and focus, but no workspace/target-pane" {
  run bash "$TOGGLE_SH" shell
  [ "$status" -eq 0 ]

  open_call="$(grep '^plugin pane open' "$STUB_HERDR_LOG")"
  [[ "$open_call" == *"--plugin maro114510.toggle-popup"* ]]
  [[ "$open_call" == *"--entrypoint shell"* ]]
  [[ "$open_call" == *"--placement overlay"* ]]
  [[ "$open_call" == *"--cwd /focused/cwd"* ]]
  [[ "$open_call" == *"--focus"* ]]
  [[ "$open_call" != *"--workspace"* ]]
  [[ "$open_call" != *"--target-pane"* ]]
}

@test "closes and clears the registry when a popup is already open" {
  state_set "workspace:ws1:shell" "pane-existing" "maro114510.toggle-popup" "shell" "workspace" "ws1" "" 1

  run bash "$TOGGLE_SH" shell
  [ "$status" -eq 0 ]

  run state_get "workspace:ws1:shell"
  [ "$status" -eq 1 ]

  grep -q "^plugin pane close pane-existing$" "$STUB_HERDR_LOG"
  ! grep -q "^plugin pane open" "$STUB_HERDR_LOG"
}

@test "a close failure clears the stale entry and opens a fresh popup" {
  state_set "workspace:ws1:shell" "pane-stale" "maro114510.toggle-popup" "shell" "workspace" "ws1" "" 1
  export STUB_HERDR_CLOSE_EXIT=1
  export STUB_HERDR_OPEN_PANE_ID="pane-fresh"

  run bash "$TOGGLE_SH" shell
  [ "$status" -eq 0 ]

  run state_get "workspace:ws1:shell"
  [ "$status" -eq 0 ]
  [ "$(echo "$output" | jq -r .pane_id)" = "pane-fresh" ]

  grep -q "^plugin pane close pane-stale$" "$STUB_HERDR_LOG"
  grep -q "^plugin pane open" "$STUB_HERDR_LOG"
}

@test "on open failure, prints a message to stderr and does not write to the registry" {
  export STUB_HERDR_OPEN_EXIT=1

  run bash "$TOGGLE_SH" shell
  [ "$status" -ne 0 ]
  [ -n "$output" ]

  [ ! -e "$POPUPS_FILE" ]
}

@test "when the focused pane's cwd cannot be determined, fails without ever calling herdr open" {
  export HERDR_PLUGIN_CONTEXT_JSON='{"workspace_id":"ws1"}'

  run bash "$TOGGLE_SH" shell
  [ "$status" -ne 0 ]
  [ -n "$output" ]

  [ ! -e "$POPUPS_FILE" ]
  ! grep -q "^plugin pane open" "$STUB_HERDR_LOG"
}

@test "falls back to a herdr found on PATH when HERDR_BIN_PATH is unset" {
  unset HERDR_BIN_PATH
  export STUB_HERDR_OPEN_PANE_ID="pane-on-path"

  run env -u HERDR_BIN_PATH PATH="$STUB_DIR:$PATH" bash "$TOGGLE_SH" shell
  [ "$status" -eq 0 ]

  run state_get "workspace:ws1:shell"
  [ "$status" -eq 0 ]
  [ "$(echo "$output" | jq -r .pane_id)" = "pane-on-path" ]
}
