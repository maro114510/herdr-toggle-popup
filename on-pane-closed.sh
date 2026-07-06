#!/usr/bin/env bash
# on-pane-closed.sh — pane.closed event hook.
# herdr's manifest [[events]] hooks fire for every pane close, not just this plugin's own popup.
# This self-filters using HERDR_PANE_ID, deleting a registry entry only when it's ours.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=./state.sh
source "${SCRIPT_DIR}/state.sh"

pane_id="${HERDR_PANE_ID:-}"
[ -n "${pane_id}" ] || exit 0

state_delete_by_pane_id "${pane_id}"
