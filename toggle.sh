#!/usr/bin/env bash
# toggle.sh — workspace-scoped popup toggle: close-on-second-press, opens at the focused pane's cwd.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=./state.sh
source "${SCRIPT_DIR}/state.sh"

PLUGIN_ID="maro114510.toggle-popup"

entrypoint="${1:?usage: toggle.sh <entrypoint>}"
herdr_bin="${HERDR_BIN_PATH:-herdr}"
workspace_id="${HERDR_WORKSPACE_ID:?HERDR_WORKSPACE_ID must be set}"

key="workspace:${workspace_id}:${entrypoint}"

# Reads the focused pane's cwd out of the plugin invocation context.
# Herdr's overlay panes always target the active pane, so this is the cwd the popup should open at.
_toggle_focused_cwd() {
  printf '%s' "${HERDR_PLUGIN_CONTEXT_JSON:-}" | jq -r '.focused_pane_cwd? // empty' 2>/dev/null || true
}

# Opens a new popup pane and, on success, registers its pane_id under $key.
# On any failure, prints a short message to stderr and leaves the registry untouched.
_toggle_open() {
  local cwd open_output pane_id

  cwd="$(_toggle_focused_cwd)"
  if [ -z "${cwd}" ]; then
    printf 'toggle.sh: could not determine the focused pane'\''s cwd\n' >&2
    return 1
  fi

  if ! open_output="$("${herdr_bin}" plugin pane open \
    --plugin "${PLUGIN_ID}" \
    --entrypoint "${entrypoint}" \
    --placement overlay \
    --cwd "${cwd}" \
    --focus 2>&1)"; then
    printf 'toggle.sh: failed to open popup pane: %s\n' "${open_output}" >&2
    return 1
  fi

  pane_id="$(printf '%s' "${open_output}" | jq -r '.result.plugin_pane.pane.pane_id? // empty' 2>/dev/null || true)"
  if [ -z "${pane_id}" ]; then
    printf 'toggle.sh: could not determine the opened pane'\''s id\n' >&2
    return 1
  fi

  state_set "${key}" "${pane_id}" "${PLUGIN_ID}" "${entrypoint}" "workspace" "${workspace_id}" "" "$(($(date +%s) * 1000))"
}

entry="$(state_get "${key}" 2>/dev/null || true)"
if [ -n "${entry}" ]; then
  stored_pane_id="$(printf '%s' "${entry}" | jq -r '.pane_id? // empty' 2>/dev/null || true)"
  if "${herdr_bin}" plugin pane close "${stored_pane_id}" >/dev/null 2>&1; then
    state_delete "${key}"
    exit 0
  fi
  state_delete "${key}"
fi

_toggle_open
