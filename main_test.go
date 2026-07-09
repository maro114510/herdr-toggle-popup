package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	t.Parallel()

	t.Run("not-yet-implemented commands", func(t *testing.T) {
		t.Parallel()

		cases := map[string]struct {
			args []string
		}{
			cmdOnPaneClosed: {args: []string{cmdOnPaneClosed}},
			cmdPopupShell:   {args: []string{cmdPopupShell}},
		}
		for name, tc := range cases {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				var stdout, stderr bytes.Buffer

				code := run(tc.args, &stdout, &stderr)

				if code == 0 {
					t.Errorf("exit code = 0, want non-zero")
				}
				if !strings.Contains(stderr.String(), "not implemented") {
					t.Errorf("stderr = %q, want it to contain %q", stderr.String(), "not implemented")
				}
			})
		}
	})

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
	for _, unwanted := range []string{"not implemented", "unknown command"} {
		if strings.Contains(stderr.String(), unwanted) {
			t.Errorf("stderr = %q, want it to not contain %q", stderr.String(), unwanted)
		}
	}
}
