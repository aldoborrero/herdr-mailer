#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2154
# Stop the daemon started by start.sh. Messages are durable in SQLite, so a
# stop never loses queued work.
set -eu
# common.sh defines mailer, state, pidfile and running_pid.
. "$(dirname "$0")/common.sh"
if ! pid=$(running_pid); then
  rm -f "$pidfile"
  echo "herdr-mailer: daemon not running"
  exit 0
fi
kill -TERM "$pid"
for _ in $(seq 50); do
  kill -0 "$pid" 2>/dev/null || break
  sleep 0.1
done
if kill -0 "$pid" 2>/dev/null; then
  echo "herdr-mailer: daemon (pid $pid) did not stop within 5s" >&2
  exit 1
fi
rm -f "$pidfile"
