#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

RUN_DIR=".run"
BIN="$RUN_DIR/priceskeptic"
PID_FILE="$RUN_DIR/priceskeptic.pid"

if [ ! -f "$PID_FILE" ]; then
  echo "priceskeptic is not running (no pid file)"
  exit 0
fi

pid="$(cat "$PID_FILE" 2>/dev/null || true)"
if [ -z "$pid" ] || ! kill -0 "$pid" 2>/dev/null; then
  echo "priceskeptic is not running (stale pid file)"
  rm -f "$PID_FILE"
  exit 0
fi

if ! ps -p "$pid" -o args= | grep -Fq "$BIN"; then
  echo "pid $pid does not look like priceskeptic; refusing to kill" >&2
  rm -f "$PID_FILE"
  exit 1
fi

kill -TERM "$pid"

for _ in $(seq 1 50); do
  if ! kill -0 "$pid" 2>/dev/null; then
    rm -f "$PID_FILE"
    echo "priceskeptic stopped"
    exit 0
  fi
  sleep 0.2
done

echo "priceskeptic did not stop after 10s; sending SIGKILL" >&2
kill -KILL "$pid" 2>/dev/null || true
rm -f "$PID_FILE"
echo "priceskeptic stopped"
