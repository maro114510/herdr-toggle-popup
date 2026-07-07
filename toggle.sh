#!/usr/bin/env bash
# toggle.sh — popup toggle: hide-on-second-press (the popup's process keeps running in a
# background tab, so its shell session survives), opens at the focused pane's cwd.
# A second argument selects how another entrypoint's already-open popup is treated (default: switch).
# Scoped by workspace by default; set scope = "directory" in $HERDR_PLUGIN_CONFIG_DIR/config.toml to share popups by the focused pane's cwd instead, across workspaces.
# Set popup_size.<entrypoint> in the same file to resize a newly opened popup toward an approximate target size; see README for the format and its limitations.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=./state.sh
source "${SCRIPT_DIR}/state.sh"
# shellcheck source=./context.sh
source "${SCRIPT_DIR}/context.sh"

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

# Reads the opt-in `popup_size.<entrypoint>` key from $HERDR_PLUGIN_CONFIG_DIR/config.toml: a space-separated list of "direction:amount:count" steps run against a newly opened popup to approximate a target size.
# Herdr's pane.resize is relative-only (no absolute-size query), so this is a best-effort approximation, not an exact match — see README.
# Prints nothing when the directory, file, or key is absent.
_toggle_size_steps() {
  local config_file key value
  [ -n "${HERDR_PLUGIN_CONFIG_DIR:-}" ] || return 0
  config_file="${HERDR_PLUGIN_CONFIG_DIR}/config.toml"
  [ -f "${config_file}" ] || return 0
  while IFS='=' read -r key value; do
    key="$(printf '%s' "${key}" | sed -E 's/^[[:space:]]+|[[:space:]]+$//g')"
    [ "${key}" = "popup_size.${entrypoint}" ] || continue
    value="$(printf '%s' "${value}" | sed -nE 's/^[[:space:]]*"([^"]*)"[[:space:]]*$/\1/p')"
    [ -n "${value}" ] && printf '%s' "${value}" && return 0
  done < "${config_file}"
}

# Best-effort: runs the configured direction:amount:count steps against the newly opened pane.
# Malformed steps are skipped individually; resize failures are ignored — the popup is already open and registered, so sizing is cosmetic and must never fail the toggle.
_toggle_apply_size() {
  local pane_id="${1}" steps step direction amount count i
  steps="$(_toggle_size_steps)"
  [ -n "${steps}" ] || return 0

  local -a step_list
  read -r -a step_list <<<"${steps}"
  for step in "${step_list[@]}"; do
    IFS=':' read -r direction amount count <<<"${step}"
    case "${direction}" in
      left | right | up | down) ;;
      *) continue ;;
    esac
    [[ "${amount}" =~ ^[0-9]+(\.[0-9]+)?$ ]] || continue
    [[ "${count}" =~ ^[1-9][0-9]*$ ]] || continue
    for ((i = 0; i < count; i++)); do
      "${herdr_bin}" pane resize --direction "${direction}" --amount "${amount}" --pane "${pane_id}" >/dev/null 2>&1 || true
    done
  done
}

# $key_prefix is the namespace force-close operates within: other entrypoints sharing the same prefix are treated as "already open in this context" and closed alongside toggling.
if [ "${scope_mode}" = "directory" ]; then
  cwd="$(context_field focused_pane_cwd)"
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

  # Herdr's overlay panes always target the active pane, so the focused pane's cwd is what the popup should open at.
  cwd="$(context_field focused_pane_cwd)"
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
  _toggle_apply_size "${pane_id}"
}

# force-close mode: closes every other entrypoint's popup registered under the same scope prefix (workspace, or directory when scope = "directory"), clearing its registry entry regardless of whether the close call succeeds.
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

# Hides an already-visible popup without terminating its process: un-zooms it (best-effort,
# since the following move is what actually matters), then moves it into a fresh background tab
# out of view. herdr's `pane move` can report success (exit 0) with changed:false instead of
# failing outright (e.g. while the pane is still zoomed), so success is read from the response
# body rather than the exit code alone.
_toggle_hide() {
  local pane_id="${1}" move_output
  "${herdr_bin}" pane zoom "${pane_id}" --off >/dev/null 2>&1 || true
  move_output="$("${herdr_bin}" pane move "${pane_id}" --new-tab --no-focus 2>/dev/null)" || return 1
  [ "$(printf '%s' "${move_output}" | jq -r '.result.move_result.changed? // false' 2>/dev/null)" = "true" ]
}

# Re-shows a hidden popup over the currently focused pane: moves it back into the focused tab as
# a split, then re-zooms it (best-effort — the pane is already visible once moved, so a failed
# zoom is cosmetic, not a reason to give up and open a fresh popup on top of it).
# Same changed-field caveat as _toggle_hide applies to the move call.
_toggle_show() {
  local pane_id="${1}" tab_id move_output
  tab_id="$(context_field tab_id)"
  [ -n "${tab_id}" ] || return 1

  move_output="$("${herdr_bin}" pane move "${pane_id}" --tab "${tab_id}" --split right --focus 2>/dev/null)" || return 1
  [ "$(printf '%s' "${move_output}" | jq -r '.result.move_result.changed? // false' 2>/dev/null)" = "true" ] || return 1

  "${herdr_bin}" pane zoom "${pane_id}" --on >/dev/null 2>&1 || true
}

entry="$(state_get "${key}" 2>/dev/null || true)"
if [ -n "${entry}" ]; then
  stored_pane_id="$(printf '%s' "${entry}" | jq -r '.pane_id? // empty' 2>/dev/null || true)"
  hidden="$(printf '%s' "${entry}" | jq -r '.hidden? // false' 2>/dev/null || true)"

  if [ "${hidden}" = "true" ]; then
    if _toggle_show "${stored_pane_id}"; then
      state_set_hidden "${key}" false
      exit 0
    fi
  else
    if _toggle_hide "${stored_pane_id}"; then
      state_set_hidden "${key}" true
      exit 0
    fi
  fi
  state_delete "${key}"
fi

if [ "${mode}" = "force-close" ]; then
  _toggle_close_other_popups
fi

_toggle_open
