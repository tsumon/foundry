#!/bin/sh
set -eu

PROJECT_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
"$PROJECT_ROOT/scripts/build.sh"
cd "$PROJECT_ROOT"

exec ./bin/foundry
