#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

required_go=$(awk '$1 == "go" { print $2; exit }' go.mod)
installed_go=$(GOTOOLCHAIN=local go env GOVERSION)
if [[ "$installed_go" != "go$required_go" ]]; then
    echo "Devcontainer Go ($installed_go) must match go.mod (go$required_go). Update .devcontainer/Dockerfile and rebuild." >&2
    exit 1
fi

go mod download
go build -o ./dm .
echo "Denmother is ready. Run bash .devcontainer/smoke.sh --with-deps for the full local preflight."
