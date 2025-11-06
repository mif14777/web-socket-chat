#!/usr/bin/env zsh
set -euo pipefail

# Run server and two GUI clients using `go run`.
# Usage: ./scripts/run_demo.sh

SCRIPT_DIR=${0:A:h}
ROOT_DIR=${SCRIPT_DIR:h}
cd "$ROOT_DIR"

pids=()

cleanup() {
  echo "\n[cleanup] stopping processes..."
  for pid in ${pids[@]:-}; do
    if kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null || true
    fi
  done
}
trap cleanup INT TERM EXIT

# 1) Start server
printf "[run] starting server...\n"
go run ./server &
pids+=($!)

# Small delay to let server start
sleep 0.8

# 2) Start first GUI client
printf "[run] starting GUI client A...\n"
FYNE_THEME=dark go run ./client_gui &
pids+=($!)

# 3) Start second GUI client
sleep 0.5
printf "[run] starting GUI client B...\n"
FYNE_THEME=dark go run ./client_gui &
pids+=($!)

# 4) Start third GUI client
sleep 0.5
printf "[run] starting GUI client C...\n"
FYNE_THEME=dark go run ./client_gui &
pids+=($!)

# 5) Wait for background jobs (press Ctrl-C to stop)
wait
