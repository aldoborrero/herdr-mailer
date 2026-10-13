#!/usr/bin/env bash
set -eu
dir=$(dirname "$0")
"$dir/stop.sh"
exec "$dir/start.sh"
