#!/usr/bin/env bats
#
# Test list (see toggle.sh):
# - open-when-empty: opens a popup and registers its pane_id under workspace:<id>:<entrypoint>
# - open call never passes --workspace/--target-pane (overlay panes target the active pane;
#   herdr rejects those flags for overlay placement)
# - hide-when-open: hides a registered, visible popup in place by zooming a sibling pane in its own
#   tab to the front (never `pane move`, which would demote the overlay to an ordinary tab pane) and
#   keeps its registry entry, marked hidden, instead of deleting it
# - show-when-hidden: re-shows a hidden popup by re-zooming it in place (`pane zoom <pane> --on`,
#   no move, no plugin-pane-focus) and marks the registry entry visible again
# - stale-pane-id-recovery: when `pane get` reports the registered pane is gone, clears the stale
#   entry and opens a fresh popup (on both the hide and show paths)
# - live-pane toggle failure: when the popup still exists but cannot be hidden (alone in its tab, so
#   no sibling) or shown (herdr call fails), it is left unchanged rather than reopened
# - open failure: prints to stderr, does not touch the registry, exits non-zero
# - missing focused-pane cwd: fails before ever invoking `herdr plugin pane open`
# - HERDR_BIN_PATH fallback: falls back to a `herdr` found on PATH when the env var is unset
# - mode=switch (default): another open entrypoint's popup is left untouched
# - mode=force-close: closes other open entrypoints' popups (same scope only) before opening
# - mode=force-close: still opens the requested popup even if closing the other one fails
# - mode=force-open: another open entrypoint's popup is left untouched
# - same-entrypoint toggle still hides it regardless of mode
# - unknown mode: rejected before touching the registry or calling herdr
#
# Directory-scoping (opt-in via scope = "directory" in $HERDR_PLUGIN_CONFIG_DIR/config.toml):
# - defaults to workspace scope when HERDR_PLUGIN_CONFIG_DIR is unset
# - defaults to workspace scope when config.toml is missing
# - defaults to workspace scope when config.toml has no scope key
# - explicit scope = "workspace" keeps workspace scoping
# - registers the popup under directory:<cwd>:<entrypoint> when scope = "directory"
# - two different cwds get independent registry entries and popups
# - the same cwd is shared across different workspaces
# - missing focused-pane cwd fails before ever invoking `herdr plugin pane open`
# - mode=force-close scopes to the current directory (not workspace) when scope = "directory"
#
# Nested/stacked popups (issue #19):
# - opening a second entrypoint's popup with mode=force-open while the first is still open
#   stacks it: both registry entries exist afterward
# - hiding the inner (second) popup afterward marks only its own registry entry hidden, leaves
#   the outer popup's entry and pane untouched, and never calls `herdr plugin pane close` on
#   either pane
#
# Configurable popup size, opt-in via popup_size.<entrypoint> in $HERDR_PLUGIN_CONFIG_DIR/config.toml:
# - no popup_size entry for the entrypoint: opens exactly as it does today, without issuing any
#   `pane resize` calls
# - configured entrypoint: issues the configured direction/amount sequence, each amount repeated
#   its configured count, in order, against the newly opened pane
# - a config entry for a different entrypoint is not applied
# - an invalid direction, a non-numeric amount, or a non-positive count skips only that step;
#   other valid steps in the same value still run
# - a `pane resize` failure does not fail toggle.sh or touch the registry
# - resize is never attempted when opening fails
# - resize is never attempted on the hide path or the show path

_write_scope_config() {
  mkdir -p "$HERDR_PLUGIN_CONFIG_DIR"
  printf 'scope = "%s"\n' "$1" > "$HERDR_PLUGIN_CONFIG_DIR/config.toml"
}

_write_size_config() {
  mkdir -p "$HERDR_PLUGIN_CONFIG_DIR"
  printf 'popup_size.%s = "%s"\n' "$1" "$2" > "$HERDR_PLUGIN_CONFIG_DIR/config.toml"
}

setup() {
  TOGGLE_SH="$BATS_TEST_DIRNAME/../toggle.sh"
  export HERDR_PLUGIN_STATE_DIR="$BATS_TEST_TMPDIR/plugin-state"
  export HERDR_PLUGIN_CONFIG_DIR="$BATS_TEST_TMPDIR/plugin-config"
  export HERDR_WORKSPACE_ID="ws1"
  export HERDR_PLUGIN_CONTEXT_JSON='{"workspace_id":"ws1","focused_pane_id":"ws1:p1","focused_pane_cwd":"/focused/cwd","tab_id":"ws1:t1"}'
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

case "$1 $2" in
  "plugin pane")
    case "$3" in
      open)
        exit_code="${STUB_HERDR_OPEN_EXIT:-0}"
        if [ "$exit_code" -eq 0 ]; then
          pane_id="${STUB_HERDR_OPEN_PANE_ID:-new-pane-1}"
          printf '{"id":"cli:plugin","result":{"plugin_pane":{"entrypoint":"shell","pane":{"pane_id":"%s","workspace_id":"ws1"},"plugin_id":"maro114510.toggle-popup"},"type":"plugin_pane_opened"}}\n' "$pane_id"
        else
          printf '{"error":{"code":"invalid_params","message":"stub open failure"},"id":"cli:plugin"}\n' >&2
        fi
        exit "$exit_code"
        ;;
      close)
        exit_code="${STUB_HERDR_CLOSE_EXIT:-0}"
        if [ "$exit_code" -eq 0 ]; then
          printf '{"id":"cli:plugin","result":{"type":"plugin_pane_closed"}}\n'
        else
          printf '{"error":{"code":"plugin_pane_not_found","message":"plugin pane not found"},"id":"cli:plugin"}\n' >&2
        fi
        exit "$exit_code"
        ;;
    esac
    ;;
  "pane get")
    exit_code="${STUB_HERDR_GET_EXIT:-0}"
    if [ "$exit_code" -eq 0 ]; then
      printf '{"id":"cli:pane:get","result":{"pane":{"pane_id":"%s"}}}\n' "$3"
    else
      printf '{"error":{"code":"pane_not_found","message":"stub get failure"},"id":"cli:pane:get"}\n' >&2
    fi
    exit "$exit_code"
    ;;
  "pane layout")
    # invoked as: pane layout --pane <pane_id>, so $4 is the queried pane.
    if [ "${STUB_HERDR_LAYOUT_SOLO:-0}" -eq 1 ]; then
      printf '{"id":"cli:pane:layout","result":{"layout":{"panes":[{"pane_id":"%s"}]}}}\n' "$4"
    else
      printf '{"id":"cli:pane:layout","result":{"layout":{"panes":[{"pane_id":"%s"},{"pane_id":"pane-sibling"}]}}}\n' "$4"
    fi
    exit 0
    ;;
  "pane resize")
    exit_code="${STUB_HERDR_RESIZE_EXIT:-0}"
    if [ "$exit_code" -eq 0 ]; then
      printf '{"id":"cli:pane:resize","result":{"type":"pane_resized"}}\n'
    else
      printf '{"error":{"code":"pane_not_found","message":"stub resize failure"},"id":"cli:pane:resize"}\n' >&2
    fi
    exit "$exit_code"
    ;;
  "pane zoom")
    exit_code="${STUB_HERDR_ZOOM_EXIT:-0}"
    if [ "$exit_code" -eq 0 ]; then
      printf '{"id":"cli:pane:zoom","result":{"zoom":{"changed":true}}}\n'
    else
      printf '{"error":{"code":"pane_not_found","message":"stub zoom failure"},"id":"cli:pane:zoom"}\n' >&2
    fi
    exit "$exit_code"
    ;;
  "pane move")
    exit_code="${STUB_HERDR_MOVE_EXIT:-0}"
    if [ "$exit_code" -eq 0 ]; then
      changed="${STUB_HERDR_MOVE_CHANGED:-true}"
      printf '{"id":"cli:pane:move","result":{"move_result":{"changed":%s}}}\n' "$changed"
    else
      printf '{"error":{"code":"pane_not_found","message":"stub move failure"},"id":"cli:pane:move"}\n' >&2
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

@test "hides an already-open popup in place (zooms a sibling) and keeps its entry marked hidden" {
  state_set "workspace:ws1:shell" "pane-existing" "maro114510.toggle-popup" "shell" "workspace" "ws1" "" 1

  run bash "$TOGGLE_SH" shell
  [ "$status" -eq 0 ]

  run state_get "workspace:ws1:shell"
  [ "$status" -eq 0 ]
  [ "$(echo "$output" | jq -r .pane_id)" = "pane-existing" ]
  [ "$(echo "$output" | jq -r .hidden)" = "true" ]

  grep -q "^pane zoom pane-sibling --on$" "$STUB_HERDR_LOG"
  ! grep -q "^pane move" "$STUB_HERDR_LOG"
  ! grep -q "^plugin pane close" "$STUB_HERDR_LOG"
  ! grep -q "^plugin pane open" "$STUB_HERDR_LOG"
}

@test "re-shows a hidden popup in place by re-zooming it and marks it visible again" {
  state_set "workspace:ws1:shell" "pane-existing" "maro114510.toggle-popup" "shell" "workspace" "ws1" "" 1
  state_set_hidden "workspace:ws1:shell" true

  run bash "$TOGGLE_SH" shell
  [ "$status" -eq 0 ]

  run state_get "workspace:ws1:shell"
  [ "$status" -eq 0 ]
  [ "$(echo "$output" | jq -r .pane_id)" = "pane-existing" ]
  [ "$(echo "$output" | jq -r .hidden)" = "false" ]

  grep -q "^pane zoom pane-existing --on$" "$STUB_HERDR_LOG"
  ! grep -q "^pane move" "$STUB_HERDR_LOG"
  ! grep -q "^plugin pane focus" "$STUB_HERDR_LOG"
  ! grep -q "^plugin pane close" "$STUB_HERDR_LOG"
  ! grep -q "^plugin pane open" "$STUB_HERDR_LOG"
}

@test "stale pane (gone): clears the entry and opens a fresh popup on the hide path" {
  state_set "workspace:ws1:shell" "pane-stale" "maro114510.toggle-popup" "shell" "workspace" "ws1" "" 1
  export STUB_HERDR_GET_EXIT=1
  export STUB_HERDR_OPEN_PANE_ID="pane-fresh"

  run bash "$TOGGLE_SH" shell
  [ "$status" -eq 0 ]

  run state_get "workspace:ws1:shell"
  [ "$status" -eq 0 ]
  [ "$(echo "$output" | jq -r .pane_id)" = "pane-fresh" ]
  [ "$(echo "$output" | jq -r '.hidden // false')" = "false" ]

  grep -q "^plugin pane open" "$STUB_HERDR_LOG"
  ! grep -q "^pane zoom" "$STUB_HERDR_LOG"
  ! grep -q "^plugin pane close" "$STUB_HERDR_LOG"
}

@test "stale pane (gone): clears the hidden entry and opens a fresh popup on the show path" {
  state_set "workspace:ws1:shell" "pane-stale" "maro114510.toggle-popup" "shell" "workspace" "ws1" "" 1
  state_set_hidden "workspace:ws1:shell" true
  export STUB_HERDR_GET_EXIT=1
  export STUB_HERDR_OPEN_PANE_ID="pane-fresh"

  run bash "$TOGGLE_SH" shell
  [ "$status" -eq 0 ]

  run state_get "workspace:ws1:shell"
  [ "$status" -eq 0 ]
  [ "$(echo "$output" | jq -r .pane_id)" = "pane-fresh" ]
  [ "$(echo "$output" | jq -r '.hidden // false')" = "false" ]

  grep -q "^plugin pane open" "$STUB_HERDR_LOG"
  ! grep -q "^pane zoom" "$STUB_HERDR_LOG"
}

@test "hide: a popup alone in its tab (no sibling) is left unchanged, not reopened" {
  state_set "workspace:ws1:shell" "pane-existing" "maro114510.toggle-popup" "shell" "workspace" "ws1" "" 1
  export STUB_HERDR_LAYOUT_SOLO=1

  run bash "$TOGGLE_SH" shell
  [ "$status" -eq 0 ]

  run state_get "workspace:ws1:shell"
  [ "$status" -eq 0 ]
  [ "$(echo "$output" | jq -r .pane_id)" = "pane-existing" ]
  [ "$(echo "$output" | jq -r '.hidden // false')" = "false" ]

  ! grep -q "^pane zoom" "$STUB_HERDR_LOG"
  ! grep -q "^pane move" "$STUB_HERDR_LOG"
  ! grep -q "^plugin pane open" "$STUB_HERDR_LOG"
  ! grep -q "^plugin pane close" "$STUB_HERDR_LOG"
}

@test "hide: a zoom failure leaves the live popup unchanged and does not reopen" {
  state_set "workspace:ws1:shell" "pane-existing" "maro114510.toggle-popup" "shell" "workspace" "ws1" "" 1
  export STUB_HERDR_ZOOM_EXIT=1

  run bash "$TOGGLE_SH" shell
  [ "$status" -eq 0 ]

  run state_get "workspace:ws1:shell"
  [ "$status" -eq 0 ]
  [ "$(echo "$output" | jq -r .pane_id)" = "pane-existing" ]
  [ "$(echo "$output" | jq -r '.hidden // false')" = "false" ]

  grep -q "^pane zoom pane-sibling --on$" "$STUB_HERDR_LOG"
  ! grep -q "^plugin pane open" "$STUB_HERDR_LOG"
}

@test "show: a zoom failure leaves the live hidden popup unchanged and does not reopen" {
  state_set "workspace:ws1:shell" "pane-existing" "maro114510.toggle-popup" "shell" "workspace" "ws1" "" 1
  state_set_hidden "workspace:ws1:shell" true
  export STUB_HERDR_ZOOM_EXIT=1

  run bash "$TOGGLE_SH" shell
  [ "$status" -eq 0 ]

  run state_get "workspace:ws1:shell"
  [ "$status" -eq 0 ]
  [ "$(echo "$output" | jq -r .pane_id)" = "pane-existing" ]
  [ "$(echo "$output" | jq -r .hidden)" = "true" ]

  grep -q "^pane zoom pane-existing --on$" "$STUB_HERDR_LOG"
  ! grep -q "^plugin pane open" "$STUB_HERDR_LOG"
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

@test "mode=switch (default) leaves another open entrypoint's popup untouched" {
  state_set "workspace:ws1:shell" "pane-a" "maro114510.toggle-popup" "shell" "workspace" "ws1" "" 1
  export STUB_HERDR_OPEN_PANE_ID="pane-b"

  run bash "$TOGGLE_SH" git
  [ "$status" -eq 0 ]

  run state_get "workspace:ws1:shell"
  [ "$status" -eq 0 ]
  [ "$(echo "$output" | jq -r .pane_id)" = "pane-a" ]

  run state_get "workspace:ws1:git"
  [ "$status" -eq 0 ]
  [ "$(echo "$output" | jq -r .pane_id)" = "pane-b" ]

  ! grep -q "^plugin pane close" "$STUB_HERDR_LOG"
}

@test "mode=force-close closes another open entrypoint's popup before opening the requested one" {
  state_set "workspace:ws1:shell" "pane-a" "maro114510.toggle-popup" "shell" "workspace" "ws1" "" 1
  export STUB_HERDR_OPEN_PANE_ID="pane-b"

  run bash "$TOGGLE_SH" git force-close
  [ "$status" -eq 0 ]

  run state_get "workspace:ws1:shell"
  [ "$status" -eq 1 ]

  run state_get "workspace:ws1:git"
  [ "$status" -eq 0 ]
  [ "$(echo "$output" | jq -r .pane_id)" = "pane-b" ]

  grep -q "^plugin pane close pane-a$" "$STUB_HERDR_LOG"
}

@test "mode=force-close still opens the requested popup even if closing the other one fails" {
  state_set "workspace:ws1:shell" "pane-a" "maro114510.toggle-popup" "shell" "workspace" "ws1" "" 1
  export STUB_HERDR_CLOSE_EXIT=1
  export STUB_HERDR_OPEN_PANE_ID="pane-b"

  run bash "$TOGGLE_SH" git force-close
  [ "$status" -eq 0 ]

  run state_get "workspace:ws1:shell"
  [ "$status" -eq 1 ]

  run state_get "workspace:ws1:git"
  [ "$status" -eq 0 ]
  [ "$(echo "$output" | jq -r .pane_id)" = "pane-b" ]
}

@test "mode=force-close only closes popups in the same workspace" {
  state_set "workspace:ws1:shell" "pane-a" "maro114510.toggle-popup" "shell" "workspace" "ws1" "" 1
  state_set "workspace:ws2:shell" "pane-other-ws" "maro114510.toggle-popup" "shell" "workspace" "ws2" "" 1
  export STUB_HERDR_OPEN_PANE_ID="pane-b"

  run bash "$TOGGLE_SH" git force-close
  [ "$status" -eq 0 ]

  ! grep -q "^plugin pane close pane-other-ws$" "$STUB_HERDR_LOG"
  run state_get "workspace:ws2:shell"
  [ "$status" -eq 0 ]
}

@test "mode=force-open leaves another open entrypoint's popup untouched" {
  state_set "workspace:ws1:shell" "pane-a" "maro114510.toggle-popup" "shell" "workspace" "ws1" "" 1
  export STUB_HERDR_OPEN_PANE_ID="pane-b"

  run bash "$TOGGLE_SH" git force-open
  [ "$status" -eq 0 ]

  run state_get "workspace:ws1:shell"
  [ "$status" -eq 0 ]
  [ "$(echo "$output" | jq -r .pane_id)" = "pane-a" ]

  run state_get "workspace:ws1:git"
  [ "$status" -eq 0 ]
  [ "$(echo "$output" | jq -r .pane_id)" = "pane-b" ]

  ! grep -q "^plugin pane close" "$STUB_HERDR_LOG"
}

@test "toggling the same entrypoint still hides it regardless of mode" {
  state_set "workspace:ws1:shell" "pane-existing" "maro114510.toggle-popup" "shell" "workspace" "ws1" "" 1

  run bash "$TOGGLE_SH" shell force-close
  [ "$status" -eq 0 ]

  run state_get "workspace:ws1:shell"
  [ "$status" -eq 0 ]
  [ "$(echo "$output" | jq -r .hidden)" = "true" ]

  grep -q "^pane zoom pane-sibling --on$" "$STUB_HERDR_LOG"
  ! grep -q "^pane move" "$STUB_HERDR_LOG"
  ! grep -q "^plugin pane close" "$STUB_HERDR_LOG"
  ! grep -q "^plugin pane open" "$STUB_HERDR_LOG"
}

@test "rejects an unknown mode without touching the registry or calling herdr" {
  run bash "$TOGGLE_SH" shell bogus-mode
  [ "$status" -ne 0 ]
  [ -n "$output" ]

  [ ! -e "$POPUPS_FILE" ]
  [ ! -s "$STUB_HERDR_LOG" ]
}

@test "defaults to workspace scope when HERDR_PLUGIN_CONFIG_DIR is unset" {
  unset HERDR_PLUGIN_CONFIG_DIR

  run bash "$TOGGLE_SH" shell
  [ "$status" -eq 0 ]

  run state_get "workspace:ws1:shell"
  [ "$status" -eq 0 ]
}

@test "defaults to workspace scope when config.toml is missing" {
  run bash "$TOGGLE_SH" shell
  [ "$status" -eq 0 ]

  run state_get "workspace:ws1:shell"
  [ "$status" -eq 0 ]
}

@test "defaults to workspace scope when config.toml has no scope key" {
  mkdir -p "$HERDR_PLUGIN_CONFIG_DIR"
  printf 'other = "value"\n' > "$HERDR_PLUGIN_CONFIG_DIR/config.toml"

  run bash "$TOGGLE_SH" shell
  [ "$status" -eq 0 ]

  run state_get "workspace:ws1:shell"
  [ "$status" -eq 0 ]
}

@test "explicit scope = \"workspace\" keeps workspace scoping" {
  _write_scope_config "workspace"

  run bash "$TOGGLE_SH" shell
  [ "$status" -eq 0 ]

  run state_get "workspace:ws1:shell"
  [ "$status" -eq 0 ]
}

@test "directory scope: registers the popup under directory:<cwd>:<entrypoint>" {
  _write_scope_config "directory"

  run bash "$TOGGLE_SH" shell
  [ "$status" -eq 0 ]

  run state_get "directory:/focused/cwd:shell"
  [ "$status" -eq 0 ]
  [ "$(echo "$output" | jq -r .scope)" = "directory" ]

  run state_get "workspace:ws1:shell"
  [ "$status" -eq 1 ]
}

@test "directory scope: two different cwds get independent registry entries and popups" {
  _write_scope_config "directory"

  export HERDR_PLUGIN_CONTEXT_JSON='{"workspace_id":"ws1","focused_pane_cwd":"/dir/a"}'
  export STUB_HERDR_OPEN_PANE_ID="pane-a"
  run bash "$TOGGLE_SH" shell
  [ "$status" -eq 0 ]

  export HERDR_PLUGIN_CONTEXT_JSON='{"workspace_id":"ws1","focused_pane_cwd":"/dir/b"}'
  export STUB_HERDR_OPEN_PANE_ID="pane-b"
  run bash "$TOGGLE_SH" shell
  [ "$status" -eq 0 ]

  run state_get "directory:/dir/a:shell"
  [ "$status" -eq 0 ]
  [ "$(echo "$output" | jq -r .pane_id)" = "pane-a" ]

  run state_get "directory:/dir/b:shell"
  [ "$status" -eq 0 ]
  [ "$(echo "$output" | jq -r .pane_id)" = "pane-b" ]
}

@test "directory scope: the same cwd is shared across different workspaces" {
  _write_scope_config "directory"

  export HERDR_WORKSPACE_ID="ws1"
  run bash "$TOGGLE_SH" shell
  [ "$status" -eq 0 ]

  run state_get "directory:/focused/cwd:shell"
  [ "$status" -eq 0 ]
  stored_pane_id="$(echo "$output" | jq -r .pane_id)"

  export HERDR_WORKSPACE_ID="ws2"
  run bash "$TOGGLE_SH" shell
  [ "$status" -eq 0 ]

  grep -q "^pane zoom pane-sibling --on\$" "$STUB_HERDR_LOG"
  ! grep -q "^pane move" "$STUB_HERDR_LOG"

  run state_get "directory:/focused/cwd:shell"
  [ "$status" -eq 0 ]
  [ "$(echo "$output" | jq -r .pane_id)" = "$stored_pane_id" ]
  [ "$(echo "$output" | jq -r .hidden)" = "true" ]
}

@test "directory scope: missing focused-pane cwd fails before ever invoking herdr open" {
  _write_scope_config "directory"
  export HERDR_PLUGIN_CONTEXT_JSON='{"workspace_id":"ws1"}'

  run bash "$TOGGLE_SH" shell
  [ "$status" -ne 0 ]
  [ -n "$output" ]

  [ ! -e "$POPUPS_FILE" ]
  ! grep -q "^plugin pane open" "$STUB_HERDR_LOG"
}

@test "nested popups: opening a second entrypoint with force-open stacks it without closing the first" {
  export STUB_HERDR_OPEN_PANE_ID="pane-outer"
  run bash "$TOGGLE_SH" shell force-open
  [ "$status" -eq 0 ]

  run state_get "workspace:ws1:shell"
  [ "$status" -eq 0 ]
  [ "$(echo "$output" | jq -r .pane_id)" = "pane-outer" ]

  export STUB_HERDR_OPEN_PANE_ID="pane-inner"
  run bash "$TOGGLE_SH" git force-open
  [ "$status" -eq 0 ]

  run state_get "workspace:ws1:shell"
  [ "$status" -eq 0 ]
  [ "$(echo "$output" | jq -r .pane_id)" = "pane-outer" ]

  run state_get "workspace:ws1:git"
  [ "$status" -eq 0 ]
  [ "$(echo "$output" | jq -r .pane_id)" = "pane-inner" ]

  ! grep -q "^plugin pane close" "$STUB_HERDR_LOG"
}

@test "nested popups: hiding the inner popup leaves the outer popup's registry entry and pane untouched" {
  export STUB_HERDR_OPEN_PANE_ID="pane-outer"
  run bash "$TOGGLE_SH" shell force-open
  [ "$status" -eq 0 ]

  export STUB_HERDR_OPEN_PANE_ID="pane-inner"
  run bash "$TOGGLE_SH" git force-open
  [ "$status" -eq 0 ]

  run bash "$TOGGLE_SH" git force-open
  [ "$status" -eq 0 ]

  run state_get "workspace:ws1:git"
  [ "$status" -eq 0 ]
  [ "$(echo "$output" | jq -r .pane_id)" = "pane-inner" ]
  [ "$(echo "$output" | jq -r .hidden)" = "true" ]

  run state_get "workspace:ws1:shell"
  [ "$status" -eq 0 ]
  [ "$(echo "$output" | jq -r .pane_id)" = "pane-outer" ]
  [ "$(echo "$output" | jq -r '.hidden // false')" = "false" ]

  grep -q "^pane zoom pane-sibling --on$" "$STUB_HERDR_LOG"
  ! grep -q "^pane move" "$STUB_HERDR_LOG"
  ! grep -q "^plugin pane close" "$STUB_HERDR_LOG"

  calls="$(cut -d' ' -f1-3 "$STUB_HERDR_LOG")"
  expected="$(printf 'plugin pane open\nplugin pane open\npane get pane-inner\npane layout --pane\npane zoom pane-sibling\n')"
  [ "$calls" = "$expected" ]
}

@test "mode=force-close scopes to the current directory, not the workspace, when scope = directory" {
  _write_scope_config "directory"
  state_set "directory:/focused/cwd:shell" "pane-a" "maro114510.toggle-popup" "shell" "directory" "ws1" "" 1
  state_set "directory:/other/cwd:shell" "pane-other-dir" "maro114510.toggle-popup" "shell" "directory" "ws1" "" 1
  export STUB_HERDR_OPEN_PANE_ID="pane-b"

  run bash "$TOGGLE_SH" git force-close
  [ "$status" -eq 0 ]

  run state_get "directory:/focused/cwd:shell"
  [ "$status" -eq 1 ]

  run state_get "directory:/other/cwd:shell"
  [ "$status" -eq 0 ]

  grep -q "^plugin pane close pane-a$" "$STUB_HERDR_LOG"
  ! grep -q "^plugin pane close pane-other-dir$" "$STUB_HERDR_LOG"
}

@test "popup size: no popup_size entry issues no pane resize calls" {
  export STUB_HERDR_OPEN_PANE_ID="pane-42"

  run bash "$TOGGLE_SH" shell
  [ "$status" -eq 0 ]

  ! grep -q "^pane resize" "$STUB_HERDR_LOG"
}

@test "popup size: runs the configured direction/amount sequence against the newly opened pane" {
  _write_size_config "shell" "right:0.5:2 down:0.25:1"
  export STUB_HERDR_OPEN_PANE_ID="pane-42"

  run bash "$TOGGLE_SH" shell
  [ "$status" -eq 0 ]

  resize_calls="$(grep "^pane resize" "$STUB_HERDR_LOG")"
  expected="$(printf 'pane resize --direction right --amount 0.5 --pane pane-42\npane resize --direction right --amount 0.5 --pane pane-42\npane resize --direction down --amount 0.25 --pane pane-42')"
  [ "$resize_calls" = "$expected" ]
}

@test "popup size: a config entry for a different entrypoint is not applied" {
  _write_size_config "git" "right:0.5:2"
  export STUB_HERDR_OPEN_PANE_ID="pane-42"

  run bash "$TOGGLE_SH" shell
  [ "$status" -eq 0 ]

  ! grep -q "^pane resize" "$STUB_HERDR_LOG"
}

@test "popup size: a malformed step is skipped, other valid steps in the same value still run" {
  _write_size_config "shell" "sideways:0.5:2 right:notanumber:2 down:0.5:0 up:0.5:1"
  export STUB_HERDR_OPEN_PANE_ID="pane-42"

  run bash "$TOGGLE_SH" shell
  [ "$status" -eq 0 ]

  resize_calls="$(grep "^pane resize" "$STUB_HERDR_LOG")"
  [ "$resize_calls" = "pane resize --direction up --amount 0.5 --pane pane-42" ]
}

@test "popup size: a pane resize failure does not fail toggle.sh or touch the registry" {
  _write_size_config "shell" "right:0.5:1"
  export STUB_HERDR_OPEN_PANE_ID="pane-42"
  export STUB_HERDR_RESIZE_EXIT=1

  run bash "$TOGGLE_SH" shell
  [ "$status" -eq 0 ]

  run state_get "workspace:ws1:shell"
  [ "$status" -eq 0 ]
  [ "$(echo "$output" | jq -r .pane_id)" = "pane-42" ]
}

@test "popup size: resize is never attempted when opening fails" {
  _write_size_config "shell" "right:0.5:1"
  export STUB_HERDR_OPEN_EXIT=1

  run bash "$TOGGLE_SH" shell
  [ "$status" -ne 0 ]

  ! grep -q "^pane resize" "$STUB_HERDR_LOG"
}

@test "popup size: resize is never attempted on the hide path" {
  _write_size_config "shell" "right:0.5:1"
  state_set "workspace:ws1:shell" "pane-existing" "maro114510.toggle-popup" "shell" "workspace" "ws1" "" 1

  run bash "$TOGGLE_SH" shell
  [ "$status" -eq 0 ]

  ! grep -q "^pane resize" "$STUB_HERDR_LOG"
}

@test "popup size: resize is never attempted on the show path" {
  _write_size_config "shell" "right:0.5:1"
  state_set "workspace:ws1:shell" "pane-existing" "maro114510.toggle-popup" "shell" "workspace" "ws1" "" 1
  state_set_hidden "workspace:ws1:shell" true

  run bash "$TOGGLE_SH" shell
  [ "$status" -eq 0 ]

  ! grep -q "^pane resize" "$STUB_HERDR_LOG"
}
