#!/bin/sh
set -eu

PROJECT_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
"$PROJECT_ROOT/scripts/build.sh"
cd "$PROJECT_ROOT"

WEB_PID=
SERVER_PID=
cleanup() {
	trap - INT TERM EXIT
	if [ -n "$SERVER_PID" ]; then
		kill -TERM "$SERVER_PID" 2>/dev/null || true
	fi
	if [ -n "$WEB_PID" ]; then
		kill -TERM "$WEB_PID" 2>/dev/null || true
	fi
	wait "$SERVER_PID" 2>/dev/null || true
	wait "$WEB_PID" 2>/dev/null || true
}
trap cleanup INT TERM EXIT

(cd web && exec ./node_modules/.bin/vite build --watch) &
WEB_PID=$!
./bin/foundry &
SERVER_PID=$!
wait "$SERVER_PID"
