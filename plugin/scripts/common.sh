# shellcheck shell=bash disable=SC2034
# Shared by the plugin scripts. The package substitutes @path@; from a source
# checkout the caller's PATH is used as is.
PATH="@path@:$PATH"
root=$(cd "$(dirname "$0")/.." && pwd)
mailer="$root/bin/herdr-mailer"
state="${HERDR_MAILER_STATE:-${XDG_STATE_HOME:-$HOME/.local/state}/herdr-mailer}"
pidfile="$state/plugin-daemon.pid"

# Prints the recorded daemon PID when that process is still a herdr-mailer.
running_pid() {
  [ -r "$pidfile" ] || return 1
  pid=$(cat "$pidfile")
  case "$pid" in '' | *[!0-9]*) return 1 ;; esac
  kill -0 "$pid" 2>/dev/null || return 1
  # Guard against PID reuse where /proc exists.
  if [ -r "/proc/$pid/cmdline" ]; then
    tr '\0' ' ' <"/proc/$pid/cmdline" | grep -q 'herdr-mailer.* daemon' || return 1
  fi
  echo "$pid"
}
