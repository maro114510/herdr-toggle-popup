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
				wantInMsg: []string{cmdToggle, cmdPopupShell},
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

// Native popups emit no pane lifecycle events and keep no registry, so the legacy overlay event
// handlers and gc were removed with the overlay surface (#148). Their names must be rejected as
// unknown commands instead of reaching a subcommand that no longer exists.
func TestRun_RemovedLegacySubcommandsAreRejectedAsUnknown(t *testing.T) {
	t.Parallel()

	for _, removed := range [...]string{"on-pane-closed", "on-tab-focused", "gc"} {
		t.Run(removed, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer

			code := run([]string{removed}, &stdout, &stderr)

			if code == 0 {
				t.Errorf("exit code = 0, want non-zero for the removed command %q", removed)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}
			if !strings.Contains(stderr.String(), "unknown command") {
				t.Errorf("stderr = %q, want it to report an unknown command", stderr.String())
			}
		})
	}
}

// Usage lists the commands a caller can run, so it must never name one that was removed.
func TestRun_UsageOmitsRemovedLegacySubcommands(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	run(nil, &stdout, &stderr)

	for _, removed := range [...]string{"on-pane-closed", "on-tab-focused", "gc"} {
		if strings.Contains(stderr.String(), removed) {
			t.Errorf("usage = %q, want it to not name the removed command %q", stderr.String(), removed)
		}
	}
}
