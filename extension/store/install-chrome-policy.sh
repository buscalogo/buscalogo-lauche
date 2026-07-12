#!/usr/bin/env bash
# Instala política Chrome/Chromium para permitir .crx auto-hospedado em extensions.bl
# (evita CRX_REQUIRED_PROOF_MISSING). Requer sudo.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
SRC="$ROOT/chrome-policy.json"

install_one() {
  local dir="$1"
  sudo mkdir -p "$dir"
  sudo cp -f "$SRC" "$dir/buscalogo-extension.json"
  sudo chmod 644 "$dir/buscalogo-extension.json"
  echo ">> $dir/buscalogo-extension.json"
}

echo ">> Política BuscaLogo (ExtensionInstallSources + Allowlist)"
[[ -f "$SRC" ]] || { echo "faltando $SRC"; exit 1; }

# Google Chrome
if [[ -d /opt/google/chrome ]] || command -v google-chrome >/dev/null 2>&1; then
  install_one /etc/opt/chrome/policies/managed
fi
# Chromium (distros variam)
if command -v chromium >/dev/null 2>&1 || command -v chromium-browser >/dev/null 2>&1; then
  install_one /etc/chromium/policies/managed 2>/dev/null || \
  install_one /etc/chromium-browser/policies/managed 2>/dev/null || true
fi

echo
echo ">> Feche TODAS as janelas do Chrome e abra de novo."
echo ">> Confira em chrome://policy — as chaves ExtensionInstall* devem aparecer."
echo ">> Depois abra http://extensions.bl:4000/ e clique em Instalar."
echo
echo "Opcional (instala sozinho, sem clique): adicione ExtensionInstallForcelist"
echo "  em chrome-policy.json apontando para updates.xml — ver HOSTING.md"
