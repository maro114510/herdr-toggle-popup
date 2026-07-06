#!/usr/bin/env bash
# state.sh — popups.json registry: read/get/set/delete with atomic writes and corruption recovery.
# Sourced by toggle.sh; requires jq and ${HERDR_PLUGIN_STATE_DIR} to be set by the caller.

_state_file() {
  : "${HERDR_PLUGIN_STATE_DIR:?HERDR_PLUGIN_STATE_DIR must be set}"
  printf '%s/popups.json' "${HERDR_PLUGIN_STATE_DIR}"
}

_state_default_registry() {
  printf '%s' '{"version":1,"popups":{}}'
}

_state_is_valid_registry() {
  jq -e '(.version == 1) and (.popups | type) == "object"' >/dev/null 2>&1
}

# Atomically writes the given JSON text as the registry: write to a temp file in the same directory,
# then mv (rename) it into place.
state_write_registry() {
  local json="${1}" file dir tmp
  file="$(_state_file)"
  dir="$(dirname "${file}")"
  mkdir -p "${dir}"
  tmp="$(mktemp "${dir}/.popups.json.tmp.XXXXXX")" || return 1
  if ! printf '%s' "${json}" > "${tmp}"; then
    rm -f "${tmp}"
    return 1
  fi
  mv "${tmp}" "${file}"
}

# Prints the current registry JSON.
# Safe on a missing file (returns the default empty registry).
# On a corrupt/malformed file, backs up the original to popups.json.bak.<unix ts>
# and reinitializes popups.json with the default empty registry.
state_read() {
  local file default
  file="$(_state_file)"
  default="$(_state_default_registry)"

  if [ ! -f "${file}" ]; then
    printf '%s' "${default}"
    return 0
  fi

  local content
  content="$(cat "${file}")"
  if printf '%s' "${content}" | _state_is_valid_registry; then
    printf '%s' "${content}"
    return 0
  fi

  local backup
  backup="${file}.bak.$(date +%s)"
  [ -e "${backup}" ] && backup="${backup}.${$}"
  if ! mv "${file}" "${backup}"; then
    printf 'state_read: failed to back up corrupt registry, aborting reset\n' >&2
    return 1
  fi
  state_write_registry "${default}"
  printf '%s' "${default}"
}

# Prints the entry JSON for the given key.
# Exits 1 (no output) if the key is not present in the registry.
state_get() {
  local key="${1}" registry entry
  registry="$(state_read)"
  entry="$(printf '%s' "${registry}" | jq -c --arg k "${key}" '.popups[$k] // empty')"
  [ -n "${entry}" ] || return 1
  printf '%s' "${entry}"
}

# Usage: state_set <key> <pane_id> <plugin_id> <entrypoint> <scope> <workspace_id> <tab_id> <created_at_unix_ms>
# workspace_id/tab_id may be passed as "" to store JSON null.
state_set() {
  local key="${1}" pane_id="${2}" plugin_id="${3}" entrypoint="${4}" scope="${5}" \
    workspace_id="${6}" tab_id="${7}" created_at_unix_ms="${8}"
  local registry entry updated
  registry="$(state_read)"
  entry="$(jq -cn \
    --arg pane_id "${pane_id}" \
    --arg plugin_id "${plugin_id}" \
    --arg entrypoint "${entrypoint}" \
    --arg scope "${scope}" \
    --arg workspace_id "${workspace_id}" \
    --arg tab_id "${tab_id}" \
    --argjson created_at_unix_ms "${created_at_unix_ms}" \
    '{
      pane_id: $pane_id,
      plugin_id: $plugin_id,
      entrypoint: $entrypoint,
      scope: $scope,
      workspace_id: (if $workspace_id == "" then null else $workspace_id end),
      tab_id: (if $tab_id == "" then null else $tab_id end),
      created_at_unix_ms: $created_at_unix_ms
    }')"
  [ -n "${entry}" ] || return 1
  updated="$(printf '%s' "${registry}" | jq -c --arg k "${key}" --argjson entry "${entry}" '.popups[$k] = $entry')"
  [ -n "${updated}" ] || return 1
  state_write_registry "${updated}"
}

# Removes the entry for the given key, if present. Idempotent.
state_delete() {
  local key="${1}" registry updated
  registry="$(state_read)"
  updated="$(printf '%s' "${registry}" | jq -c --arg k "${key}" 'del(.popups[$k])')"
  state_write_registry "${updated}"
}

# Removes every entry whose pane_id matches, regardless of key. Idempotent.
state_delete_by_pane_id() {
  local pane_id="${1}" registry updated
  registry="$(state_read)"
  updated="$(printf '%s' "${registry}" | jq -c --arg pane_id "${pane_id}" \
    '.popups |= with_entries(select(.value.pane_id != $pane_id))')"
  state_write_registry "${updated}"
}
