#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"

RUN_DIR=".run"
BIN="$RUN_DIR/prisjeger"
PID_FILE="$RUN_DIR/prisjeger.pid"

if [ ! -f "$PID_FILE" ]; then
  echo "prisjeger is not running (no pid file)"
  exit 0
fi

pid="$(cat "$PID_FILE" 2>/dev/null || true)"
if [ -z "$pid" ] || ! kill -0 "$pid" 2>/dev/null; then
  echo "prisjeger is not running (stale pid file)"
  rm -f "$PID_FILE"
  exit 0
fi

if ! ps -p "$pid" -o args= | grep -Fq "$BIN"; then
  echo "pid $pid does not look like prisjeger; refusing to kill" >&2
  rm -f "$PID_FILE"
  exit 1
fi

kill -TERM "$pid"

for _ in $(seq 1 50); do
  if ! kill -0 "$pid" 2>/dev/null; then
    rm -f "$PID_FILE"
    echo "prisjeger stopped"
    exit 0
  fi
  sleep 0.2
done

echo "prisjeger did not stop after 10s; sending SIGKILL" >&2
kill -KILL "$pid" 2>/dev/null || true
rm -f "$PID_FILE"
echo "prisjeger stopped"
