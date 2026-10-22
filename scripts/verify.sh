#!/bin/sh
set -eu

PROJECT_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$PROJECT_ROOT"

require_exact_version() {
	name=$1
	actual=$2
	expected=$3
	if [ "$actual" != "$expected" ]; then
		echo "$name version mismatch: expected $expected, received $actual" >&2
		exit 1
	fi
}

command -v go >/dev/null 2>&1 || { echo "go was not found" >&2; exit 1; }
command -v node >/dev/null 2>&1 || { echo "node was not found" >&2; exit 1; }
command -v npm >/dev/null 2>&1 || { echo "npm was not found" >&2; exit 1; }
command -v cc >/dev/null 2>&1 || { echo "a C compiler was not found" >&2; exit 1; }
[ -x /usr/bin/git ] || { echo "/usr/bin/git was not found" >&2; exit 1; }

require_exact_version "Go" "$(go env GOVERSION)" "go1.27.3"
require_exact_version "Node.js" "$(node --version)" "v22.23.2"
require_exact_version "npm" "$(npm --version)" "10.9.8"
/usr/bin/git --no-lazy-fetch --version
cc --version

VERIFY_TEMP=$(mktemp -d "${TMPDIR:-/tmp}/foundry-verify.XXXXXX")
trap 'rm -rf "$VERIFY_TEMP"' EXIT HUP INT TERM
: >"$VERIFY_TEMP/user.npmrc"
: >"$VERIFY_TEMP/global.npmrc"

export CGO_ENABLED=1
export GIT_CONFIG_GLOBAL=/dev/null
export GIT_CONFIG_NOSYSTEM=1
export GIT_NO_LAZY_FETCH=1
export NPM_CONFIG_GLOBALCONFIG="$VERIFY_TEMP/global.npmrc"
export NPM_CONFIG_USERCONFIG="$VERIFY_TEMP/user.npmrc"

go mod download
go mod verify
node --test scripts/export-alpha.test.mjs
go test -count=1 -timeout=600s ./...
go test -race -count=1 -timeout=1200s ./...
go vet ./...
(
	cd web
	npm ci
	npm test -- --run
)
./scripts/build.sh
