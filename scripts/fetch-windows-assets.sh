#!/usr/bin/env bash
# Baixa binários Windows amd64 (yggdrasil + coredns) para assets/windows/.
# Uso: ./scripts/fetch-windows-assets.sh
# Ou:  make build-windows  (chama este script se os .exe estiverem ausentes)
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="${ROOT}/assets/windows"
YGG_VERSION="${YGG_VERSION:-0.5.14}"
DNS_VERSION="${DNS_VERSION:-1.14.4}"
WINTUN_VERSION="${WINTUN_VERSION:-0.14.1}"
WINTUN_SHA256="${WINTUN_SHA256:-07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51}"
CLEANUPS=()
cleanup() { for d in "${CLEANUPS[@]:-}"; do rm -rf "$d"; done; }
trap cleanup EXIT

mkdir -p "$OUT"

echo ">> assets/windows — yggdrasil v${YGG_VERSION} + coredns v${DNS_VERSION} + wintun v${WINTUN_VERSION}"

# --- CoreDNS (zip oficial no GitHub) ---
if [[ ! -s "${OUT}/coredns.exe" ]]; then
  echo "   baixando coredns windows_amd64..."
  TMP="$(mktemp -d)"
  CLEANUPS+=("$TMP")
  curl -fsSL -o "${TMP}/coredns.zip" \
    "https://github.com/coredns/coredns/releases/download/v${DNS_VERSION}/coredns_${DNS_VERSION}_windows_amd64.zip"
  unzip -o -q "${TMP}/coredns.zip" -d "$TMP"
  found="$(find "$TMP" -name 'coredns.exe' -print -quit || true)"
  if [[ -z "$found" || ! -s "$found" ]]; then
    echo ">> ERRO: coredns.exe não encontrado no zip" >&2
    exit 1
  fi
  cp -f "$found" "${OUT}/coredns.exe"
  echo "   coredns.exe: $(du -h "${OUT}/coredns.exe" | cut -f1)"
else
  echo "   coredns.exe já presente"
fi

# --- Yggdrasil ---
# Release oficial Windows é MSI (sem .exe solto). Preferimos cross-compile com Go.
if [[ ! -s "${OUT}/yggdrasil.exe" ]]; then
  if command -v go >/dev/null 2>&1; then
    echo "   compilando yggdrasil windows/amd64 com Go..."
    TMPGO="$(mktemp -d)"
    CLEANUPS+=("$TMPGO")
    (
      set -e
      cd "$TMPGO"
      go mod init yggfetch >/dev/null
      go get "github.com/yggdrasil-network/yggdrasil-go/cmd/yggdrasil@v${YGG_VERSION}"
      GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
        go build -o "${OUT}/yggdrasil.exe" \
        "github.com/yggdrasil-network/yggdrasil-go/cmd/yggdrasil"
    ) || echo "   go build falhou" >&2
  fi

  if [[ ! -s "${OUT}/yggdrasil.exe" ]]; then
    echo "   tentando extrair yggdrasil.exe do MSI..."
    MSI_URL="https://github.com/yggdrasil-network/yggdrasil-go/releases/download/v${YGG_VERSION}/yggdrasil-${YGG_VERSION}-x64.msi"
    TMPMSI="$(mktemp -d)"
    CLEANUPS+=("$TMPMSI")
    curl -fsSL -o "${TMPMSI}/ygg.msi" "$MSI_URL"
    mkdir -p "${TMPMSI}/out"
    if command -v 7z >/dev/null 2>&1; then
      7z e -y -o"${TMPMSI}/out" "${TMPMSI}/ygg.msi" 2>/dev/null || true
    elif command -v msiextract >/dev/null 2>&1; then
      msiextract "${TMPMSI}/ygg.msi" -C "${TMPMSI}/out" >/dev/null || true
    else
      echo ">> Aviso: instale Go ou 7z para obter yggdrasil.exe" >&2
      echo ">> URL: $MSI_URL" >&2
    fi
    # MSI interno usa nomes sem .exe (Yggdrasil / Yggdrasilctl).
    found="$(find "${TMPMSI}/out" \( -iname 'yggdrasil.exe' -o -name 'Yggdrasil' \) -type f -print -quit 2>/dev/null || true)"
    if [[ -n "$found" && -s "$found" ]]; then
      cp -f "$found" "${OUT}/yggdrasil.exe"
    fi
  fi

  if [[ -s "${OUT}/yggdrasil.exe" ]]; then
    echo "   yggdrasil.exe: $(du -h "${OUT}/yggdrasil.exe" | cut -f1)"
  else
    echo ">> AVISO: yggdrasil.exe ausente — build Windows compila, mas Has(yggdrasil)=false" >&2
  fi
else
  echo "   yggdrasil.exe já presente"
fi

# --- Wintun (TUN Windows — obrigatório para yggdrasil) ---
# Oficial: https://www.wintun.net/ — DLL amd64 ao lado do yggdrasil.exe.
if [[ ! -s "${OUT}/wintun.dll" ]]; then
  echo "   baixando wintun ${WINTUN_VERSION} (amd64)..."
  TMPW="$(mktemp -d)"
  CLEANUPS+=("$TMPW")
  curl -fsSL -o "${TMPW}/wintun.zip" \
    "https://www.wintun.net/builds/wintun-${WINTUN_VERSION}.zip"
  if command -v sha256sum >/dev/null 2>&1; then
    got="$(sha256sum "${TMPW}/wintun.zip" | awk '{print $1}')"
    if [[ "$got" != "$WINTUN_SHA256" ]]; then
      echo ">> ERRO: checksum wintun.zip inválido (got=$got want=$WINTUN_SHA256)" >&2
      exit 1
    fi
  fi
  unzip -o -q "${TMPW}/wintun.zip" -d "$TMPW"
  found="$(find "$TMPW" -path '*/amd64/wintun.dll' -print -quit || true)"
  if [[ -z "$found" || ! -s "$found" ]]; then
    echo ">> ERRO: wintun.dll amd64 não encontrado no zip" >&2
    exit 1
  fi
  cp -f "$found" "${OUT}/wintun.dll"
  echo "   wintun.dll: $(du -h "${OUT}/wintun.dll" | cut -f1)"
else
  echo "   wintun.dll já presente"
fi

cat > "${OUT}/MANIFEST" <<EOF
# Binários bundled do BuscaLogo Agent (Windows amd64).
# Populado por: scripts/fetch-windows-assets.sh
YGG_VERSION=${YGG_VERSION}
DNS_VERSION=${DNS_VERSION}
WINTUN_VERSION=${WINTUN_VERSION}
EOF

echo ">> Concluído: $OUT"
ls -lh "$OUT" || true
