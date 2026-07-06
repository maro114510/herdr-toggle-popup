#!/usr/bin/env bash
# context.sh — reads fields out of $HERDR_PLUGIN_CONTEXT_JSON, the plugin invocation context Herdr
# passes to entrypoints (focused pane cwd, workspace/tab ids, and more, depending on invocation).
# Sourced by toggle.sh and any future entrypoint that needs a field from it.

# Prints the named field's value from $HERDR_PLUGIN_CONTEXT_JSON, or empty string if absent.
context_field() {
  local field="${1:?context_field: field name required}"
  printf '%s' "${HERDR_PLUGIN_CONTEXT_JSON:-}" | jq -r --arg f "${field}" '.[$f]? // empty' 2>/dev/null || true
}
