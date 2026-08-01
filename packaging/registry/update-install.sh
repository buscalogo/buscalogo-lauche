#!/bin/bash
# Usado pelo auto-update quando o registry foi instalado via .deb.
# Uso: update-install.sh /caminho/pacote.deb
set -euo pipefail
DEB="${1:-}"
if [[ -z "$DEB" || ! -f "$DEB" ]]; then
  echo "uso: update-install.sh /caminho/pacote.deb" >&2
  exit 1
fi
dpkg -i "$DEB"
if command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload || true
  systemctl restart buscalogo-registry.service || true
fi
