#!/usr/bin/env bash
# Check, build and install rig after changing it, then check the stack.
#
#   ./dev-install.sh
#
# Stops at the first failing step, so a broken build is never installed.
# Changed generated files show up in the final "rig doctor" (fix them
# with "rig doctor --fix"); install-level changes need "rig install".
set -euo pipefail
cd "$(dirname "$0")"

if [[ $EUID -eq 0 ]]; then
	echo "dev-install: run as yourself, not root" >&2
	exit 1
fi

step() { printf '\n==> %s\n' "$*"; }

step "gofmt"
unformatted=$(gofmt -l .)
if [[ -n $unformatted ]]; then
	echo "not formatted (run: gofmt -w .):" >&2
	echo "$unformatted" >&2
	exit 1
fi

step "go vet"
go vet ./...

step "go test"
go test ./...

step "govulncheck"
if ! command -v govulncheck >/dev/null; then
	echo "govulncheck not found (install: mise use -g go:golang.org/x/vuln/cmd/govulncheck)" >&2
	exit 1
fi
govulncheck ./...

step "build"
# -trimpath keeps this machine's folder names out of the binary.
go build -trimpath -o bin/rig ./cmd/rig

step "install to ~/.local/bin/rig"
install -m 0755 bin/rig "$HOME/.local/bin/rig"

step "rig doctor"
if ! "$HOME/.local/bin/rig" doctor; then
	echo
	echo "Installed, but rig doctor found problems. If they are generated files, run: rig doctor --fix"
	exit 1
fi
