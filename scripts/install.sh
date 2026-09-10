#!/usr/bin/env bash
# Compatibility entry point; use setup for integrated binary and skill installation.
set -euo pipefail
root="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
exec python3 "$root/scripts/bootstrap.py" --binary-only "$@"
