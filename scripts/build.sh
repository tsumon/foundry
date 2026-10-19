#!/bin/sh
set -eu

PROJECT_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$PROJECT_ROOT"

if [ -n "${FOUNDRY_GO:-}" ]; then
	GO_COMMAND=$FOUNDRY_GO
else
	GO_COMMAND=$(command -v go 2>/dev/null || true)
fi

if [ -z "$GO_COMMAND" ] || ! command -v "$GO_COMMAND" >/dev/null 2>&1; then
	echo "Go was not found. Put go on PATH or set FOUNDRY_GO to the Go executable." >&2
	exit 1
fi
if ! command -v npm >/dev/null 2>&1; then
	echo "npm was not found; Node.js and npm are required to build the workbench UI." >&2
	exit 1
fi
if [ ! -x web/node_modules/.bin/vite ] || [ ! -x web/node_modules/.bin/tsc ]; then
	echo "Frontend dependencies are missing. Run: cd web && npm ci" >&2
	exit 1
fi

(cd web && npm run build)
make FOUNDRY_GO="$GO_COMMAND" build
