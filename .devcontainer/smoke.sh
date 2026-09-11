#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

# The same source, browser, runtime matrix, and Homebrew checks used by CI.
exec python3 -B scripts/acceptance.py preflight "$@"
