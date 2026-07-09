#!/bin/bash
set -euo pipefail
DEB="$1"
if [ -z "${DEB:-}" ] || [ ! -f "$DEB" ]; then
  echo "uso: update-install.sh /caminho/pacote.deb" >&2
  exit 1
fi
dpkg -i "$DEB"
