#!/usr/bin/env bash
# Empacota buscalogo-agent-server_VERSION_amd64.deb (headless + systemd).
# Uso:
#   ./scripts/package-agent-server-deb.sh              # build + deb
#   BINARY=./buscalogo-agent ./scripts/package-agent-server-deb.sh  # só empacota
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PKG_DIR="$ROOT/packaging/agent-server"
VERSION="${VERSION:-$(tr -d ' \n' < "$ROOT/VERSION" 2>/dev/null || echo 0.0.0)}"
COMMIT="${COMMIT:-$(git -C "$ROOT" rev-parse --short HEAD 2>/dev/null || echo unknown)}"
LDFLAGS="-X buscalogo-agent/internal/version.Version=${VERSION} -X buscalogo-agent/internal/version.Commit=${COMMIT}"
STAGE="$ROOT/dist/deb-agent-server-amd64"
OUT_DEB="$ROOT/dist/buscalogo-agent-server_${VERSION}_amd64.deb"
BINARY="${BINARY:-}"
DAEMON_BIN=buscalogo-agentd

MAKEFILE_DIR="$ROOT/"
EXTEN_DIR="$ROOT/extension"
if [[ ! -f "$EXTEN_DIR/chrome/manifest.json" ]]; then
  EXTEN_DIR="$(cd "$ROOT/../exten" 2>/dev/null && pwd || true)"
fi
if [[ ! -f "${EXTEN_DIR:-}/chrome/manifest.json" ]] || [[ ! -f "${EXTEN_DIR:-}/firefox/manifest.json" ]]; then
  echo "extensões Chrome/Firefox não encontradas (extension/ ou ../exten)" >&2
  exit 1
fi

mkdir -p "$ROOT/dist"

if [[ -z "$BINARY" ]]; then
  if [[ ! -x "$ROOT/assets/linux/coredns" ]] || [[ ! -x "$ROOT/assets/linux/yggdrasil" ]]; then
    echo "→ assets amd64…"
    make -C "$ROOT" assets ASSET_ARCH=amd64
  fi
  if [[ ! -d "$ROOT/assets/linux/couchdb/bin" ]]; then
    echo "→ assets-couchdb…"
    make -C "$ROOT" assets-couchdb
  fi
  BINARY="$ROOT/dist/buscalogo-agentd-server-${VERSION}"
  echo "→ build agent linux/amd64…"
  (
    cd "$ROOT"
    GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
      go build -ldflags "$LDFLAGS" -o "$BINARY" ./cmd/agent
  )
  chmod +x "$BINARY"
elif [[ -f "$ROOT/$BINARY" ]]; then
  BINARY="$ROOT/$BINARY"
elif [[ ! -f "$BINARY" ]]; then
  echo "binário não encontrado: $BINARY" >&2
  exit 1
fi

echo "→ staging $STAGE"
rm -rf "$STAGE"
mkdir -p \
  "$STAGE/DEBIAN" \
  "$STAGE/opt/buscalogo/data/bin" \
  "$STAGE/opt/buscalogo/certs" \
  "$STAGE/opt/buscalogo/extension" \
  "$STAGE/lib/systemd/system" \
  "$STAGE/usr/local/bin"

cp -f "$BINARY" "$STAGE/opt/buscalogo/$DAEMON_BIN"
chmod 755 "$STAGE/opt/buscalogo/$DAEMON_BIN"
cp -f "$PKG_DIR/update-install.sh" "$STAGE/opt/buscalogo/update-install.sh"
chmod 755 "$STAGE/opt/buscalogo/update-install.sh"
cp -f "$PKG_DIR/config.example.yaml" "$STAGE/opt/buscalogo/config.example.yaml"
cp -f "$PKG_DIR/buscalogo-agent.service" "$STAGE/lib/systemd/system/buscalogo-agent.service"
echo server > "$STAGE/opt/buscalogo/.install-flavor"

cp -R "$ROOT/www" "$STAGE/opt/buscalogo/"
cp -R "$ROOT/sites" "$STAGE/opt/buscalogo/"
cp -a "$EXTEN_DIR/chrome" "$STAGE/opt/buscalogo/extension/chrome"
cp -a "$EXTEN_DIR/firefox" "$STAGE/opt/buscalogo/extension/firefox"
if command -v zip >/dev/null 2>&1; then
  (cd "$STAGE/opt/buscalogo/extension" && zip -qr chrome.zip chrome && zip -qr firefox.zip firefox) || true
fi
test -f "$STAGE/opt/buscalogo/extension/chrome/manifest.json"
test -f "$STAGE/opt/buscalogo/extension/firefox/manifest.json"

if [[ -f "$ROOT/assets/icons/logo.png" ]]; then
  cp -f "$ROOT/assets/icons/logo.png" "$STAGE/opt/buscalogo/buscalogo-agent.png"
fi
ROOTCA_SRC="$ROOT/internal/ca/certs/rootCA.pem"
if [[ -f "$ROOTCA_SRC" ]]; then
  cp -f "$ROOTCA_SRC" "$STAGE/opt/buscalogo/certs/rootCA.pem"
  chmod 644 "$STAGE/opt/buscalogo/certs/rootCA.pem"
fi

cp -f "$ROOT/assets/linux/coredns" "$STAGE/opt/buscalogo/data/bin/coredns"
cp -f "$ROOT/assets/linux/yggdrasil" "$STAGE/opt/buscalogo/data/bin/yggdrasil"
chmod 755 "$STAGE/opt/buscalogo/data/bin/coredns" "$STAGE/opt/buscalogo/data/bin/yggdrasil"
if [[ -d "$ROOT/assets/linux/couchdb/bin" ]]; then
  cp -a "$ROOT/assets/linux/couchdb" "$STAGE/opt/buscalogo/data/bin/couchdb"
fi

ln -sf "/opt/buscalogo/$DAEMON_BIN" "$STAGE/usr/local/bin/$DAEMON_BIN"

sed \
  -e "s/^Version: .*/Version: ${VERSION}/" \
  -e "s/^Architecture: .*/Architecture: amd64/" \
  "$PKG_DIR/control" > "$STAGE/DEBIAN/control"
cp -f "$PKG_DIR/postinst" "$STAGE/DEBIAN/postinst"
cp -f "$PKG_DIR/prerm" "$STAGE/DEBIAN/prerm"
cp -f "$PKG_DIR/postrm" "$STAGE/DEBIAN/postrm"
chmod 755 "$STAGE/DEBIAN/postinst" "$STAGE/DEBIAN/prerm" "$STAGE/DEBIAN/postrm"

INSTALLED_SIZE="$(du -sk "$STAGE" | awk '{print $1}')"
echo "Installed-Size: ${INSTALLED_SIZE}" >> "$STAGE/DEBIAN/control"

(cd "$ROOT/dist" && fakeroot dpkg-deb -Zgzip --build "$(basename "$STAGE")" "$(basename "$OUT_DEB")")
echo "→ $OUT_DEB"
dpkg-deb -I "$OUT_DEB" | head -25
ls -lh "$OUT_DEB"
