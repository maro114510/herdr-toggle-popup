#!/usr/bin/env bats

setup() {
  POPUP_SHELL="$BATS_TEST_DIRNAME/../popup-shell.sh"
}

@test "execs \$SHELL when set, replacing the script process (same PID, no wrapper remains)" {
  local stub="$BATS_TEST_TMPDIR/stub-shell"
  local pidfile="$BATS_TEST_TMPDIR/pid"
  cat > "$stub" <<STUB
#!/usr/bin/env bash
echo "\$\$" > "$pidfile"
STUB
  chmod +x "$stub"

  SHELL="$stub" bash "$POPUP_SHELL" &
  local script_pid=$!
  wait "$script_pid"

  [ "$(cat "$pidfile")" -eq "$script_pid" ]
}

@test "execs /bin/zsh when SHELL is unset" {
  [ -x /bin/zsh ] || skip "/bin/zsh not available on this system"

  run env -u SHELL bash -c 'printf "echo ZSHV=\$ZSH_VERSION\n" | bash "$1"' _ "$POPUP_SHELL"

  [ "$status" -eq 0 ]
  [[ "$output" =~ ZSHV=[0-9] ]]
}
