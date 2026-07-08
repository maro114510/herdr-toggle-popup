// Package herdr wraps every herdr CLI invocation toggle.sh makes and the
// $HERDR_PLUGIN_CONTEXT_JSON invocation-context lookup, so callers never
// touch os/exec or raw JSON themselves.
package herdr

import (
	"encoding/json"
	"os"
)

const contextEnvVar = "HERDR_PLUGIN_CONTEXT_JSON"

// ContextField returns the named top-level string field from
// $HERDR_PLUGIN_CONTEXT_JSON. It returns "" — never an error — when the env
// var is unset, the JSON is malformed, the field is absent, or the field is
// present but not a string, matching context.sh's context_field.
func ContextField(field string) string {
	raw := os.Getenv(contextEnvVar)
	if raw == "" {
		return ""
	}

	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return ""
	}

	value, _ := doc[field].(string)
	return value
}
