#!/usr/bin/env bash
# Sincroniza ../exten → extension/ (fonte de desenvolvimento → pacote).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SRC="${1:-$ROOT/../exten}"
DST="$ROOT/extension"

if [[ ! -f "$SRC/chrome/manifest.json" ]]; then
  echo ">> Fonte não encontrada: $SRC/chrome/manifest.json" >&2
  exit 1
fi

rm -rf "$DST"
mkdir -p "$DST"
cp -a "$SRC/chrome" "$DST/chrome"
cp -a "$SRC/firefox" "$DST/firefox"
echo ">> Sincronizado $SRC → $DST"
find "$DST" -type f | sort
