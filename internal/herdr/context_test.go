package herdr

import "testing"

// Test list (mirrors tests/context.bats):
// - returns a field's value when present
// - returns empty string when the field is absent
// - returns empty string when HERDR_PLUGIN_CONTEXT_JSON is unset
// - returns empty string when HERDR_PLUGIN_CONTEXT_JSON is malformed JSON
// - reads distinct fields independently out of the same JSON
// - returns empty string when the field is present but not a string

func TestContextField_ReturnsValueWhenPresent(t *testing.T) {
	t.Setenv(contextEnvVar, `{"focused_pane_cwd":"/focused/cwd"}`)

	if got := ContextField("focused_pane_cwd"); got != "/focused/cwd" {
		t.Errorf("ContextField() = %q, want %q", got, "/focused/cwd")
	}
}

func TestContextField_AbsentField(t *testing.T) {
	t.Setenv(contextEnvVar, `{"workspace_id":"ws1"}`)

	if got := ContextField("focused_pane_cwd"); got != "" {
		t.Errorf("ContextField() = %q, want empty", got)
	}
}

func TestContextField_UnsetEnv(t *testing.T) {
	t.Setenv(contextEnvVar, "")

	if got := ContextField("focused_pane_cwd"); got != "" {
		t.Errorf("ContextField() = %q, want empty", got)
	}
}

func TestContextField_MalformedJSON(t *testing.T) {
	t.Setenv(contextEnvVar, "not-json")

	if got := ContextField("focused_pane_cwd"); got != "" {
		t.Errorf("ContextField() = %q, want empty", got)
	}
}

func TestContextField_DistinctFieldsIndependently(t *testing.T) {
	t.Setenv(contextEnvVar, `{"workspace_id":"ws1","focused_pane_id":"ws1:p1","focused_pane_cwd":"/focused/cwd"}`)

	cases := map[string]string{
		"workspace_id":     "ws1",
		"focused_pane_id":  "ws1:p1",
		"focused_pane_cwd": "/focused/cwd",
		"tab_id":           "",
	}
	for field, want := range cases {
		if got := ContextField(field); got != want {
			t.Errorf("ContextField(%q) = %q, want %q", field, got, want)
		}
	}
}

func TestContextField_NonStringValue(t *testing.T) {
	t.Setenv(contextEnvVar, `{"count":3}`)

	if got := ContextField("count"); got != "" {
		t.Errorf("ContextField() = %q, want empty", got)
	}
}
