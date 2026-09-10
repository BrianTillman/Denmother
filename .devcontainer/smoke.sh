#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

go build -o ./dm .
go test ./...
go vet ./...
python3 -B scripts/check_public_source.py

# The feature starts the nested daemon asynchronously when the container starts.
for ((attempt = 0; attempt < 30; attempt++)); do
    if docker info >/dev/null 2>&1; then
        break
    fi
    sleep 1
done
docker info >/dev/null
docker compose version

# A temporary copy exercises nested bind mounts without editing the checkout.
scratch=$(mktemp -d)
config="$scratch/quickstart/ha-config"
cleanup() {
    ./dm dev down --config "$config" --json
    # These volumes belong only to the disposable copy created by this invocation.
    for compose in "$scratch"/quickstart/.devcontainer/worktrees/*/docker-compose.yml; do
        [[ -f "$compose" ]] || continue
        docker compose -p "$(basename "$(dirname "$compose")")" -f "$compose" down --volumes
    done
    rm -rf "$scratch"
}
trap cleanup EXIT
mkdir -p "$scratch/quickstart"
cp -R examples/quickstart/ha-config examples/quickstart/docs "$scratch/quickstart/"
./dm dev up --config "$config" --json
./dm --config "$config"
./dm test --config "$config" --json

# Verify a diagnostic failure as well as the successful runtime path.
sed -i 's/input_boolean.study_lamp/input_boolean.study_missing_lamp/g' \
    "$config/automations/presence/motion_lamp.yaml"
if ./dm --config "$config" --no-schema >"$scratch/expected-failure.txt" 2>&1; then
    echo "Expected validation to reject the intentionally missing lamp." >&2
    exit 1
fi
cat "$scratch/expected-failure.txt"
grep -q 'study_missing_lamp' "$scratch/expected-failure.txt"
