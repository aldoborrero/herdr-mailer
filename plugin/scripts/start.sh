#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2154
# Start the mailer daemon in the background and exit, as Herdr expects of a
# startup command.
set -eu
# common.sh defines mailer, state, pidfile and running_pid.
. "$(dirname "$0")/common.sh"
if [ ! -x "$mailer" ]; then
  echo "herdr-mailer: $mailer is missing" >&2
  exit 1
fi
if [ ! -f "$state/owner.json" ]; then
  echo "herdr-mailer: $state is not initialized; run 'herdr-mailer init' first" >&2
  exit 0
fi
if pid=$(running_pid); then
  echo "herdr-mailer: daemon already running (pid $pid)"
  exit 0
fi
umask 077
nohup "$mailer" --state "$state" daemon >>"$state/daemon.log" 2>&1 &
echo $! >"$pidfile"
