#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

# Use the CLI's pinned browser version so setup and rendering share a download.
playwright_version=$(sed -n 's/^const dashboardRenderPlaywrightVersion = "\([^"]*\)"/\1/p' cmd/dev_dashboard.go)
if [[ ! "$playwright_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "Cannot determine the pinned Playwright version from cmd/dev_dashboard.go." >&2
    exit 1
fi

# Playwright uses sudo for system libraries and keeps Chromium in this user's cache.
npm exec --yes --package="playwright@$playwright_version" -- playwright install --with-deps chromium
