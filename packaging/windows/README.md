# Staging + MSI build notes (Windows)

## Linux / CI: gerar staging

```bash
make msi-stage
# → dist/msi-stage/{buscalogo-agentd.exe,buscalogo-agent.exe,resources.neu,trayIcon.png,wintun.dll}
```

## Windows: gerar MSI

Requisitos: [WiX Toolset v4/v5](https://wixtoolset.org/) (`winget install WiXToolset.WiX`).

```powershell
# Com staging já copiado (ex.: de make msi-stage)
.\packaging\windows\build.ps1
# → dist\BuscaLogoAgent-<VERSION>-amd64.msi
```

## GitHub Actions (recomendado)

No push de tag `v*` (ex.: `git tag v0.1.14 && git push origin v0.1.14`), o workflow [Release](../../.github/workflows/release.yml):

1. gera o `.deb` (Linux)
2. gera o `.msi` no runner `windows-latest` (WiX + Neutralino)
3. publica ambos no GitHub Release

Teste manual sem tag: Actions → **Release** → **Run workflow** (só sobe artifacts; release só em tags).

## O que o MSI faz

| Item | Valor |
|------|-------|
| Install dir | `C:\Program Files\BuscaLogo\` |
| Dados | `%ProgramData%\BuscaLogo` (`BUSCALOGO_HOME` no serviço) |
| Serviço | `BuscaLogoAgent` → `buscalogo-agentd.exe --service` |
| Atalho painel | BuscaLogo Agent (Neutralino) |
| Atalho bandeja | BuscaLogo Agent (bandeja) → `--tray-ui` |

Desinstalação remove Program Files e o serviço; **preserva** ProgramData (upgrade-friendly).

## Checklist de teste

1. Instalar MSI como Administrador.
2. `sc query BuscaLogoAgent` → RUNNING.
3. http://127.0.0.1:9970 responde.
4. Abrir atalho Neutralino → painel; “Fechar” não para o serviço.
5. Abrir atalho bandeja → systray; Fechar bandeja → serviço continua.
6. Wintun/Ygg sobe (evento TUN); portas 80/443/4401 no Firewall (`BuscaLogo Ygg`).
7. Desinstalar → serviço some; dados em ProgramData permanecem.
