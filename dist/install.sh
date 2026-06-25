#!/bin/bash
set -e

# Instalador do BuscaLogo Agent (portável)
# Uso: sudo ./install.sh [caminho-instalacao]

INSTALL_DIR="${1:-/opt/buscalogo}"
BIN_DIR="/usr/local/bin"
DESKTOP_DIR="$HOME/.local/share/applications"
AUTOSTART_DIR="$HOME/.config/autostart"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "Instalando BuscaLogo Agent em $INSTALL_DIR..."

mkdir -p "$INSTALL_DIR"
cp -R "$SCRIPT_DIR/www" "$INSTALL_DIR/"
cp -R "$SCRIPT_DIR/sites" "$INSTALL_DIR/"
cp "$SCRIPT_DIR/buscalogo-agent" "$INSTALL_DIR/"
cp "$SCRIPT_DIR/buscalogo-agent.png" "$INSTALL_DIR/"
chmod -R a+rX "$INSTALL_DIR"

# Link simbólico global
mkdir -p "$BIN_DIR"
ln -sf "$INSTALL_DIR/buscalogo-agent" "$BIN_DIR/buscalogo-agent"

# Capabilities (necessárias para portas privilegiadas)
if command -v setcap >/dev/null 2>&1; then
    setcap cap_net_bind_service=+ep "$INSTALL_DIR/buscalogo-agent" || echo "Aviso: não foi possível aplicar cap_net_bind_service (ignore se não usar porta 80)"
fi

# Desktop entry (menu de aplicativos)
mkdir -p "$DESKTOP_DIR"
cat > "$DESKTOP_DIR/buscalogo-agent.desktop" <<EOF
[Desktop Entry]
Type=Application
Name=BuscaLogo Agent
Exec=$INSTALL_DIR/buscalogo-agent
Icon=$INSTALL_DIR/buscalogo-agent.png
Terminal=false
Comment=BuscaLogo Agent — DNS, Yggdrasil e sites .bl
Categories=Network;System;
EOF

# Autostart
mkdir -p "$AUTOSTART_DIR"
cp "$DESKTOP_DIR/buscalogo-agent.desktop" "$AUTOSTART_DIR/buscalogo-agent.desktop"

echo "Instalação concluída."
echo "  Binário: $INSTALL_DIR/buscalogo-agent"
echo "  Comando: buscalogo-agent"
echo "  Painel:  http://127.0.0.1:9970"
echo ""
echo "O agente foi adicionado à inicialização automática."
echo "Para iniciar agora: buscalogo-agent"
