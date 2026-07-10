#!/usr/bin/env bash
# Empacota zips para Chrome Web Store / Edge e Firefox AMO.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DIST="$ROOT/store/dist"
VER="$(python3 -c "import json; print(json.load(open('$ROOT/chrome/manifest.json'))['version'])")"

rm -rf "$DIST"
mkdir -p "$DIST"

pack() {
  local name="$1" src="$2"
  local zip="$DIST/buscalogo-agent-${name}-${VER}.zip"
  rm -f "$zip"
  (
    cd "$src"
    zip -qr "$zip" . \
      -x '*.DS_Store' \
      -x '*~' \
      -x '*.map'
  )
  echo ">> $zip ($(du -h "$zip" | cut -f1))"
  unzip -l "$zip" | grep -E 'manifest.json|background.js|icons/' | head -20
}

pack chrome "$ROOT/chrome"
pack firefox "$ROOT/firefox"

echo ">> Pronto. Envie os zips conforme extension/store/STORE.md"
