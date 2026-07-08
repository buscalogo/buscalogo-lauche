# BuscaLogo Desktop (Neutralinojs)

Interface desktop nativa para o BuscaLogo Agent, usando [Neutralinojs](https://neutralino.js.org/).

O app desktop:
- inicia o `buscalogo-agent --no-tray` se o painel ainda não estiver rodando;
- abre o painel web em `http://127.0.0.1:9970` numa janela nativa;
- oferece ícone na bandeja do sistema (Linux/Windows) com atalhos.

## Pré-requisitos

- `neu` instalado globalmente: `npm install -g @neutralinojs/neu`
- Binário Go compilado na raiz do projeto: `make build`

## Desenvolvimento

```bash
# Na raiz do repositório
make desktop-run
```

Ou manualmente:

```bash
make build
cd desktop/buscalogo-desktop && neu run
```

## Build de produção

```bash
make desktop-build
```

Gera `desktop/buscalogo-desktop/dist/` com `buscalogo-desktop` + `buscalogo-agent` empacotados.

## Arquitetura

```
buscalogo-desktop (Neutralino)  →  janela + systray
        ↓ spawn
buscalogo-agent --no-tray       →  API + serviços em :9970
```

O painel web continua sendo o mesmo (`frontend/` embutido no agente Go). O Neutralino é apenas o shell nativo.
