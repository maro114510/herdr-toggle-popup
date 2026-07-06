#!/usr/bin/env bash
# toggle.sh — popup toggle: close-on-second-press, opens at the focused pane's cwd.
# A second argument selects how another entrypoint's already-open popup is treated (default: switch).
# Scoped by workspace by default; set scope = "directory" in $HERDR_PLUGIN_CONFIG_DIR/config.toml
# to share popups by the focused pane's cwd instead, across workspaces.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=./state.sh
source "${SCRIPT_DIR}/state.sh"

PLUGIN_ID="maro114510.toggle-popup"

entrypoint="${1:?usage: toggle.sh <entrypoint> [switch|force-close|force-open]}"
mode="${2:-switch}"
herdr_bin="${HERDR_BIN_PATH:-herdr}"
workspace_id="${HERDR_WORKSPACE_ID:?HERDR_WORKSPACE_ID must be set}"

case "${mode}" in
  switch | force-close | force-open) ;;
  *)
    printf 'toggle.sh: invalid mode: %s (expected switch, force-close, or force-open)\n' "${mode}" >&2
    exit 1
    ;;
esac

# Reads the focused pane's cwd out of the plugin invocation context.
# Herdr's overlay panes always target the active pane, so this is the cwd the popup should open at.
_toggle_focused_cwd() {
  printf '%s' "${HERDR_PLUGIN_CONTEXT_JSON:-}" | jq -r '.focused_pane_cwd? // empty' 2>/dev/null || true
}

# Reads the opt-in `scope` key from $HERDR_PLUGIN_CONFIG_DIR/config.toml.
# Defaults to "workspace" when the directory, file, or key is absent.
_toggle_scope_mode() {
  local config_file value
  [ -n "${HERDR_PLUGIN_CONFIG_DIR:-}" ] || { printf 'workspace'; return; }
  config_file="${HERDR_PLUGIN_CONFIG_DIR}/config.toml"
  [ -f "${config_file}" ] || { printf 'workspace'; return; }
  value="$(sed -nE 's/^[[:space:]]*scope[[:space:]]*=[[:space:]]*"([^"]*)"[[:space:]]*$/\1/p' "${config_file}" | head -n1)"
  printf '%s' "${value:-workspace}"
}

scope_mode="$(_toggle_scope_mode)"

# $key_prefix is the namespace force-close operates within: other entrypoints sharing the
# same prefix are treated as "already open in this context" and closed alongside toggling.
if [ "${scope_mode}" = "directory" ]; then
  cwd="$(_toggle_focused_cwd)"
  if [ -z "${cwd}" ]; then
    printf 'toggle.sh: could not determine the focused pane'\''s cwd\n' >&2
    exit 1
  fi
  key_prefix="directory:${cwd}:"
else
  key_prefix="workspace:${workspace_id}:"
fi
key="${key_prefix}${entrypoint}"

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

  state_set "${key}" "${pane_id}" "${PLUGIN_ID}" "${entrypoint}" "${scope_mode}" "${workspace_id}" "" "$(($(date +%s) * 1000))"
}

# force-close mode: closes every other entrypoint's popup registered under the same
# scope prefix (workspace, or directory when scope = "directory"), clearing its registry
# entry regardless of whether the close call succeeds.
_toggle_close_other_popups() {
  local registry other_key other_pane_id
  registry="$(state_read)"
  while IFS=$'\t' read -r other_key other_pane_id; do
    [ -z "${other_key}" ] && continue
    "${herdr_bin}" plugin pane close "${other_pane_id}" >/dev/null 2>&1 || true
    state_delete "${other_key}"
  done < <(printf '%s' "${registry}" | jq -r --arg prefix "${key_prefix}" --arg exclude "${key}" \
    '.popups | to_entries[] | select(.key | startswith($prefix)) | select(.key != $exclude) | "\(.key)\t\(.value.pane_id)"')
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

if [ "${mode}" = "force-close" ]; then
  _toggle_close_other_popups
fi

_toggle_open
