#!/usr/bin/env bash
# Empacota buscalogo-registry_VERSION_ARCH.deb com systemd.
# Uso:
#   ./scripts/package-registry-deb.sh                 # amd64 (build + deb)
#   ARCH=arm64 ./scripts/package-registry-deb.sh      # arm64
#   BINARY=dist/foo ARCH=amd64 ./scripts/package-registry-deb.sh  # só empacota
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PKG_DIR="$ROOT/packaging/registry"
VERSION="${VERSION:-$(tr -d ' \n' < "$ROOT/VERSION" 2>/dev/null || echo 0.0.0)}"
COMMIT="${COMMIT:-$(git -C "$ROOT" rev-parse --short HEAD 2>/dev/null || echo unknown)}"
ARCH_IN="${ARCH:-amd64}"
case "$ARCH_IN" in
  aarch64|arm64) GOARCH=arm64; DEB_ARCH=arm64; ASSET_ARCH=arm64 ;;
  x86_64|amd64)  GOARCH=amd64; DEB_ARCH=amd64; ASSET_ARCH=amd64 ;;
  *) echo "ARCH inválida: $ARCH_IN (amd64|arm64)" >&2; exit 1 ;;
esac

LDFLAGS="-X buscalogo-agent/internal/version.Version=${VERSION} -X buscalogo-agent/internal/version.Commit=${COMMIT}"
STAGE="$ROOT/dist/deb-registry-${DEB_ARCH}"
OUT_DEB="$ROOT/dist/buscalogo-registry_${VERSION}_${DEB_ARCH}.deb"
BINARY="${BINARY:-}"

mkdir -p "$ROOT/dist"

if [[ -z "$BINARY" ]]; then
  echo "→ assets ($ASSET_ARCH)…"
  make -C "$ROOT" assets ASSET_ARCH="$ASSET_ARCH"
  BINARY="$ROOT/dist/buscalogo-registry_${VERSION}_linux_${GOARCH}"
  echo "→ build registry linux/${GOARCH}…"
  (
    cd "$ROOT"
    GOOS=linux GOARCH="$GOARCH" CGO_ENABLED=0 \
      go build -tags registry -ldflags "$LDFLAGS" -o "$BINARY" ./cmd/registry
  )
  chmod +x "$BINARY"
fi

if [[ ! -x "$BINARY" && ! -f "$BINARY" ]]; then
  echo "binário não encontrado: $BINARY" >&2
  exit 1
fi

CFG_SRC="$ROOT/../registry/config.example.yaml"
if [[ ! -f "$CFG_SRC" ]]; then
  CFG_SRC="$PKG_DIR/config.example.yaml"
fi

echo "→ staging $STAGE"
rm -rf "$STAGE"
mkdir -p \
  "$STAGE/DEBIAN" \
  "$STAGE/opt/buscalogo-registry" \
  "$STAGE/lib/systemd/system" \
  "$STAGE/usr/local/bin"

cp -f "$BINARY" "$STAGE/opt/buscalogo-registry/buscalogo-registry"
chmod 755 "$STAGE/opt/buscalogo-registry/buscalogo-registry"
cp -f "$PKG_DIR/update-install.sh" "$STAGE/opt/buscalogo-registry/update-install.sh"
chmod 755 "$STAGE/opt/buscalogo-registry/update-install.sh"
if [[ -f "$CFG_SRC" ]]; then
  cp -f "$CFG_SRC" "$STAGE/opt/buscalogo-registry/config.example.yaml"
fi
cp -f "$PKG_DIR/buscalogo-registry.service" "$STAGE/lib/systemd/system/buscalogo-registry.service"
ln -sf /opt/buscalogo-registry/buscalogo-registry "$STAGE/usr/local/bin/buscalogo-registry"

sed \
  -e "s/^Version: .*/Version: ${VERSION}/" \
  -e "s/^Architecture: .*/Architecture: ${DEB_ARCH}/" \
  "$PKG_DIR/control" > "$STAGE/DEBIAN/control"
cp -f "$PKG_DIR/postinst" "$STAGE/DEBIAN/postinst"
cp -f "$PKG_DIR/prerm" "$STAGE/DEBIAN/prerm"
cp -f "$PKG_DIR/postrm" "$STAGE/DEBIAN/postrm"
chmod 755 "$STAGE/DEBIAN/postinst" "$STAGE/DEBIAN/prerm" "$STAGE/DEBIAN/postrm"

# Tamanho instalado aproximado (kB)
INSTALLED_SIZE="$(du -sk "$STAGE" | awk '{print $1}')"
echo "Installed-Size: ${INSTALLED_SIZE}" >> "$STAGE/DEBIAN/control"

# -Zgzip: Orange Pi / Armbian antigos não leem control.tar.zst (default do dpkg novo)
(cd "$ROOT/dist" && fakeroot dpkg-deb -Zgzip --build "$(basename "$STAGE")" "$(basename "$OUT_DEB")")
echo "→ $OUT_DEB"
dpkg-deb -I "$OUT_DEB" | head -20
ls -lh "$OUT_DEB"
