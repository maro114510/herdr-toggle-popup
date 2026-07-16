package main

import (
	"bytes"
	"strings"
	"testing"
)

// assertDispatched fails the test if stderr looks like it fell through to the not-implemented
// or unknown-command branches instead of reaching the dispatched subcommand.
func assertDispatched(t *testing.T, stderr string) {
	t.Helper()

	for _, unwanted := range [...]string{"not implemented", "unknown command"} {
		if strings.Contains(stderr, unwanted) {
			t.Errorf("stderr = %q, want it to not contain %q", stderr, unwanted)
		}
	}
}

func TestRun(t *testing.T) {
	t.Parallel()

	t.Run("invalid input", func(t *testing.T) {
		t.Parallel()

		cases := map[string]struct {
			args      []string
			wantInMsg []string
		}{
			"no args": {
				args:      nil,
				wantInMsg: []string{cmdToggle, cmdOnPaneClosed, cmdPopupShell},
			},
			"unknown command": {
				args:      []string{"unknown-cmd"},
				wantInMsg: []string{"unknown-cmd"},
			},
		}
		for name, tc := range cases {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				var stdout, stderr bytes.Buffer

				code := run(tc.args, &stdout, &stderr)

				if code == 0 {
					t.Errorf("exit code = 0, want non-zero")
				}
				if stdout.Len() != 0 {
					t.Errorf("stdout = %q, want empty", stdout.String())
				}
				for _, want := range tc.wantInMsg {
					if !strings.Contains(stderr.String(), want) {
						t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
					}
				}
			})
		}
	})
}

// toggle is wired to internal/toggle.Run (see internal/toggle for its own test suite); this only
// asserts main.go actually dispatches to it rather than falling through to the not-implemented
// or unknown-command branches.
func TestRun_ToggleDispatchesToToggleSubcommand(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	code := run([]string{cmdToggle}, &stdout, &stderr)

	if code == 0 {
		t.Errorf("exit code = 0, want non-zero for a toggle call missing its entrypoint arg")
	}
	assertDispatched(t, stderr.String())
}

// on-pane-closed is wired to internal/onpaneclosed.Run (see internal/onpaneclosed for its own
// test suite); this only asserts main.go actually dispatches to it, using a missing-state-dir
// error to get an onpaneclosed-specific message without needing a real registry.
func TestRun_OnPaneClosedDispatchesToOnPaneClosedSubcommand(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", "")
	t.Setenv("HERDR_PANE_ID", "pane-1")

	var stdout, stderr bytes.Buffer

	code := run([]string{cmdOnPaneClosed}, &stdout, &stderr)

	if code == 0 {
		t.Errorf("exit code = 0, want non-zero when HERDR_PLUGIN_STATE_DIR is unset")
	}
	assertDispatched(t, stderr.String())
	if !strings.Contains(stderr.String(), "on-pane-closed") {
		t.Errorf("stderr = %q, want an on-pane-closed-prefixed error, confirming dispatch", stderr.String())
	}
}

// on-tab-focused is wired to internal/ontabfocused.Run (see internal/ontabfocused for its own
// test suite); this only asserts main.go actually dispatches to it, using a missing-state-dir
// error to get an ontabfocused-specific message without needing a real registry.
func TestRun_OnTabFocusedDispatchesToOnTabFocusedSubcommand(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", "")
	t.Setenv("HERDR_TAB_ID", "tab-1")

	var stdout, stderr bytes.Buffer

	code := run([]string{cmdOnTabFocused}, &stdout, &stderr)

	if code == 0 {
		t.Errorf("exit code = 0, want non-zero when HERDR_PLUGIN_STATE_DIR is unset")
	}
	assertDispatched(t, stderr.String())
	if !strings.Contains(stderr.String(), "on-tab-focused") {
		t.Errorf("stderr = %q, want an on-tab-focused-prefixed error, confirming dispatch", stderr.String())
	}
}

// popup-shell is wired to internal/popupshell.Run (see internal/popupshell for its own test
// suite, including one that proves it really execs and replaces the process); this only
// asserts main.go actually dispatches to it. A nonexistent $SHELL fails before any exec is
// attempted, so this stays a safe in-process test.
func TestRun_PopupShellDispatchesToPopupShellSubcommand(t *testing.T) {
	t.Setenv("SHELL", "/nonexistent/definitely-not-a-shell")

	var stdout, stderr bytes.Buffer

	code := run([]string{cmdPopupShell}, &stdout, &stderr)

	if code == 0 {
		t.Errorf("exit code = 0, want non-zero for a nonexistent $SHELL")
	}
	assertDispatched(t, stderr.String())
	if !strings.Contains(stderr.String(), "popup-shell") {
		t.Errorf("stderr = %q, want a popup-shell-prefixed error, confirming dispatch", stderr.String())
	}
}

func TestRun_VersionPrintsVersionToStdout(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	code := run([]string{cmdVersion}, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
	if !strings.Contains(stdout.String(), version) {
		t.Errorf("stdout = %q, want it to contain %q", stdout.String(), version)
	}
}

func TestRun_DoctorDispatchesToDoctorSubcommand(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	code := run([]string{cmdDoctor}, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
	if !strings.Contains(stdout.String(), "toggle-popup diagnostics") {
		t.Errorf("stdout = %q, want doctor output", stdout.String())
	}
}
