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
  local tmp
  tmp="$(mktemp -d)"
  rm -f "$zip"
  cp -a "$src/." "$tmp/"
  # Chrome Web Store rejeita update_url (só vale para CRX auto-hospedado).
  if [[ -f "$tmp/manifest.json" ]]; then
    python3 -c "
import json
p = '$tmp/manifest.json'
m = json.load(open(p))
m.pop('update_url', None)
json.dump(m, open(p, 'w'), indent=2, ensure_ascii=False)
open(p, 'a').write('\n')
"
  fi
  (
    cd "$tmp"
    zip -qr "$zip" . \
      -x '*.DS_Store' \
      -x '*~' \
      -x '*.map'
  )
  rm -rf "$tmp"
  echo ">> $zip ($(du -h "$zip" | cut -f1))"
  unzip -l "$zip" | grep -E 'manifest.json|background.js|icons/' | head -20
}

pack chrome "$ROOT/chrome"
pack firefox "$ROOT/firefox"

echo ">> Pronto. Envie os zips conforme extension/store/STORE.md"
