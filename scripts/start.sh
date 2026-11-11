#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

RUN_DIR=".run"
BIN="$RUN_DIR/priceskeptic"
PID_FILE="$RUN_DIR/priceskeptic.pid"
LOG_FILE="$RUN_DIR/priceskeptic.log"

mkdir -p "$RUN_DIR"

if [ -f "$RUN_DIR/telegram.env" ]; then
  set -a
  . "$RUN_DIR/telegram.env"
  set +a
fi

if [ -f "$PID_FILE" ]; then
  pid="$(cat "$PID_FILE" 2>/dev/null || true)"
  if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null && ps -p "$pid" -o args= | grep -Fq "$BIN"; then
    echo "priceskeptic is already running (pid $pid)"
    echo "log: $LOG_FILE"
    exit 0
  fi
  rm -f "$PID_FILE"
fi

go build -o "$BIN" ./cmd/priceskeptic

nohup "$BIN" "$@" >> "$LOG_FILE" 2>&1 &
pid=$!
echo "$pid" > "$PID_FILE"

sleep 1
if ! kill -0 "$pid" 2>/dev/null; then
  echo "priceskeptic failed to start; last log lines:" >&2
  tail -n 20 "$LOG_FILE" >&2 || true
  rm -f "$PID_FILE"
  exit 1
fi

addr="${PRICESKEPTIC_ADDR:-127.0.0.1:8080}"
prev=""
for arg in "$@"; do
  case "$prev" in
    -addr) addr="$arg" ;;
  esac
  case "$arg" in
    -addr=*) addr="${arg#-addr=}" ;;
  esac
  prev="$arg"
done
case "$addr" in
  :*) addr="127.0.0.1$addr" ;;
  0.0.0.0:*) addr="127.0.0.1:${addr#0.0.0.0:}" ;;
esac

echo "priceskeptic started (pid $pid)"
echo "log: $LOG_FILE"
echo "status: curl -s http://$addr/status"
echo "results: curl -s http://$addr/results"
