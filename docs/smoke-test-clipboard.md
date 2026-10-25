# Host clipboard smoke test (macOS)

Automated tests verify the boundary the plugin controls: a mouse drag fills the tmux buffer, and tmux emits an OSC52 sequence carrying the selected text to its client terminal. They cannot prove that the terminal hosting Herdr forwarded that sequence to the operating system clipboard, because that last hop belongs to the terminal emulator and the OS.

Run this procedure on macOS after a change to the tmux attach script, the supported tmux versions, the close key, or the clipboard documentation. It uses a real Herdr popup and reads the result back with `pbpaste`.

## Prerequisites

| Requirement | Check |
| --- | --- |
| Herdr 0.9.3 or newer | Required version in the [README](../README.md) |
| tmux 3.4 or newer | `tmux -V` |
| The plugin is installed and the toggle key is bound | `Alt+L` opens the popup |
| `pbpaste` and `pbcopy` are available | `command -v pbpaste pbcopy` |

The popup must run a terminal that advertises the `Ms` terminfo capability. `Ms` is what tells tmux the outer terminal can accept an OSC52 clipboard write. Without it tmux keeps the selection in its own buffer and emits nothing, so a successful drag must never be reported as a clipboard success in that environment.

## Procedure

1. Put a sentinel in the host clipboard, so an unchanged clipboard is distinguishable from a copy that worked:

   ```bash
   printf 'clipboard-not-updated' | pbcopy
   ```

2. Open the popup with `Alt+L`.

3. In the popup, read the terminal name tmux sees for this client and confirm it has `Ms`:

   ```bash
   term=$(tmux display -p '#{client_termname}')
   printf 'client terminal: %s\n' "$term"
   infocmp -x "$term" | grep -o 'Ms=[^,]*'
   ```

   **Stop here if no `Ms=` line is printed.** The outer terminal cannot receive OSC52, so the host clipboard cannot update. Record a failure instead of a successful clipboard test.

4. Confirm the dedicated server sends copies outward:

   ```bash
   tmux show-options -s set-clipboard
   ```

   Expected: `set-clipboard external`.

5. Print the text to copy:

   ```bash
   printf 'herdr-clipboard-smoke\n'
   ```

6. Drag-select that line with the mouse and release. Use tmux selection; do not use Herdr native selection.

7. Read the tmux buffer inside the popup:

   ```bash
   tmux show-buffer
   ```

8. Read the host clipboard from a terminal outside Herdr, not from the popup:

   ```bash
   pbpaste
   ```

## Expected results

| Step | Expected |
| --- | --- |
| 3 | Prints an `Ms=` line for the popup's client terminal |
| 4 | Prints `set-clipboard external` |
| 7 | Prints exactly `herdr-clipboard-smoke` |
| 8 | Prints exactly `herdr-clipboard-smoke`, not `clipboard-not-updated` |

## Failure interpretation

| Symptom | Likely cause |
| --- | --- |
| Step 3 has no `Ms=` line | The outer terminal's terminfo lacks `Ms`, so tmux cannot send the copy outward. The clipboard claim does not hold in this environment. |
| Step 7 is wrong or empty | The drag did not become a tmux selection. Check that mouse mode is on in the popup and that no full-screen application inside the pane owns mouse reporting. |
| Step 7 is right but step 8 shows the sentinel | The tmux buffer path works but the OSC52 boundary does not. Recheck step 3 and the outer terminal's clipboard support. |
| The popup or tmux server disappears on release | tmux below 3.4 on Linux with ncurses 6.4 crashes while emitting the OSC52 copy. Upgrade to tmux 3.4 or newer. |
| Step 8 contains the text but Herdr's own selection also runs | The pane's application enabled mouse reporting, so Herdr native selection should stay out of the popup. Check the application or the Herdr `ui.copy_on_select` setting. |

## Related coverage

- `internal/popupshell/mouse_clipboard_test.go` drives a real mouse drag through a PTY. It asserts the tmux buffer contents and decodes the emitted OSC52 payload, and it proves that a client without `Ms` emits no OSC52.
- The `tmux-compat` CI job runs those tests against tmux 3.4 and 3.8 with `HERDR_TEST_REQUIRE_TMUX=1`, so a missing tmux fails the job instead of skipping the coverage.
