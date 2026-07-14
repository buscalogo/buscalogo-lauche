# BuscaLogo Agent — Windows (amd64)

Cross-compile no Linux (não altera o build `.deb`/Linux).

## Build

```bash
# Baixa yggdrasil.exe + coredns.exe para assets/windows/ (se faltarem) e compila
make build-windows
# → dist/buscalogo-agent.exe
```

Ou:

```bash
./scripts/fetch-windows-assets.sh
GOOS=windows GOARCH=amd64 go build -ldflags "$(make -s -f Makefile -n build 2>/dev/null; true)" -o dist/buscalogo-agent.exe ./cmd/agent
```

## No PC Windows (Agent sozinho)

1. Copie `buscalogo-agent.exe` (e opcionalmente pasta `www/` / `sites/`).
2. Rode **como Administrador** na primeira vez (TUN Ygg + portas 80/443).
3. Dados em `%USERPROFILE%\.buscalogo\` (ou `BUSCALOGO_HOME`).
4. Painel: http://127.0.0.1:9970

## Desktop Neutralino (janela + bandeja)

No Linux (com `neu` instalado: `npm i -g @neutralinojs/neu`):

```bash
make desktop-build-windows
# → dist/buscalogo-agent-win/  e  dist/buscalogo-agent-win-amd64.zip
```

No PC Windows, descompacte e rode `buscalogo-agent.exe` (shell Neutralino). Ele inicia `buscalogo-agentd.exe` e abre o painel. **Admin** na 1ª vez.

## Instalador MSI (serviço Windows)

```bash
# No Linux: gera staging (Agent + Neutralino + wintun)
make msi-stage
# → dist/msi-stage/
```

No **Windows** (WiX Toolset v4/v5: `winget install WiXToolset.WiX`):

```powershell
.\packaging\windows\build.ps1
# → dist\BuscaLogoAgent-<VERSION>-amd64.msi
```

Ou no GitHub: tag `v*` → Actions Release publica `BuscaLogoAgent-<versão>-amd64.msi` junto com o `.deb`.

O MSI instala em `C:\Program Files\BuscaLogo\`, cria o serviço **BuscaLogoAgent** (`buscalogo-agentd.exe --service`), dados em `%ProgramData%\BuscaLogo`, e atalhos:

| Atalho | Função |
|--------|--------|
| BuscaLogo Agent | Neutralino (painel); não para o serviço ao fechar |
| BuscaLogo Agent (bandeja) | `buscalogo-agentd.exe --tray-ui` |

Detalhes e checklist: [packaging/windows/README.md](../packaging/windows/README.md).

## O que difere do Linux

| Item | Windows |
|------|---------|
| CouchDB embutido | Não (~108MB). Conta em ficheiros; índice de scrape em **SQLite** (`data/scrape/index.sqlite`) |
| Overview | Card **Índice SQLite** no lugar do CouchDB |
| Conta local | Funciona sem CouchDB (cadastro, chave ed25519, assinatura de domínios) |
| Scraper / P2P busca | SQLite local (sem serviço :5984) |
| Yggdrasil admin | `tcp://127.0.0.1:9901` (não usa unix socket) |
| MSI / serviço | `BuscaLogoAgent` (LocalSystem); flag `--service` / UI `--tray-ui` |
| Dados (MSI) | `%ProgramData%\BuscaLogo` via `BUSCALOGO_HOME` |
| TUN | Requer `wintun.dll` (bundled; oficial [wintun.net](https://www.wintun.net/)). Corra o Agent **como Administrador** na 1ª vez para instalar o driver |
| Portas .bl | Dica de Firewall do Windows (não ufw/tun0). Auto-teste Ygg é menos fiável que no Linux |
| DNS Modo B | CoreDNS em `127.0.0.1:53` + NRPT `.bl` → `127.0.0.1` (painel → DNS → Ativar). Requer Admin. Depois `https://algo.bl` resolve no browser |
| Extensão | Botão Web Store abre o navegador padrão mesmo sem Chrome no PATH |
| setcap | Não existe — use Admin |
| Assets | `yggdrasil.exe` + `coredns.exe` + `wintun.dll` embutidos no `.exe` |

## Linux permanece igual

```bash
make build          # Agent Linux
make deb            # pacote .deb
make desktop-build  # Neutralino Linux
```
