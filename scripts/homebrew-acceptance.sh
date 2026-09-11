#!/usr/bin/env bash
# Run only in a disposable Homebrew container or CI runner.
set -euo pipefail
archives=$(cd "${1:?release archive directory required}" && pwd)
export HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_NO_INSTALL_CLEANUP=1 HOMEBREW_NO_ANALYTICS=1
tap="denmother/acceptance-$$"
installed=false
cleanup() {
  if "$installed"; then brew uninstall --force "$tap/denmother"; fi
  brew untap "$tap"
}
if brew list --versions denmother >/dev/null 2>&1; then
  echo "Use a disposable Homebrew: denmother is already installed." >&2
  exit 1
fi
brew tap-new --no-git "$tap"
trap cleanup EXIT
formula="$(brew --repository "$tap")/Formula/denmother.rb"
# Preserve platform selection and hashes; curl can fetch local file URLs.
sed -E "s|https://github.com/[^/]+/[^/]+/releases/download/[^/]+/|file://$archives/|g" \
  "$archives/homebrew/Formula/denmother.rb" > "$formula"
brew install --formula "$tap/denmother"
installed=true
brew test --verbose "$tap/denmother"
dm version
