# BuscaLogo Agent (Launcher)

**Idiomas:** [English](README.md) · [Português](README.pt.md) · [Español](README.es.md) · [日本語](README.ja.md)

Daemon local da rede de busca descentralizada [BuscaLogo](https://buscalogo.com). Um único binário em Go gerencia mesh, DNS de sites `.bl`, scraping, armazenamento, busca P2P, painel web e integração opcional com desktop/navegador.

## O que faz

| Componente | Função |
|------------|--------|
| **Yggdrasil** | Rede mesh overlay (peers + IPv6) |
| **CoreDNS** | Resolve domínios `.bl` (e afins) localmente |
| **CouchDB** | Guarda scrapes, usuários e config |
| **Scraper** | Crawler nativo em Go; indexa páginas no CouchDB |
| **Busca P2P** | Consulta outros Agents via signaling |
| **Conta** | Cadastro/login local; scrapes assinados com ed25519 |
| **Painel web** | UI de controle em `http://127.0.0.1:9970` |
| **Desktop** | Shell Neutralino + bandeja do sistema |
| **Extensão** | Chip de status no Chrome/Firefox + sugerir indexação |

## Requisitos

- **Linux** amd64 (alvo principal; `.deb` para Ubuntu 22.04+ / Debian 12+)
- **Go** 1.26+ (para compilar)
- Para desktop: Node.js + [`@neutralinojs/neu`](https://neutralino.js.org/)

## Início rápido

### Instalar com `.deb`

```bash
sudo dpkg -i buscalogo-agent_*_amd64.deb
buscalogo-agent
```

Abra o painel: [http://127.0.0.1:9970](http://127.0.0.1:9970)

### Compilar do código

```bash
# Opcional: baixar binários Yggdrasil, CoreDNS, CouchDB
make assets

make build          # → ./buscalogo-agent
make run            # compila e executa

# Tarball portátil
make dist

# Pacote completo (agent + desktop Neutralino + extensões)
make deb
```

### Desktop (Neutralino)

```bash
npm install -g @neutralinojs/neu
make desktop-run      # desenvolvimento
make desktop-build    # bundle de produção
```

Veja [desktop/buscalogo-desktop/README.md](desktop/buscalogo-desktop/README.md).

## Primeiro uso

1. Inicie o Agent (ou o app desktop).
2. Sem conta → tela de **cadastro**.
3. Com conta, sem sessão → tela de **login** (app bloqueado até autenticar).
4. A sessão persiste em `data/identity/session.json` (sobrevive a reinícios).
5. Exporte o **backup JSON** (inclui chave privada) na aba Perfil — guarde em local seguro.

Os scrapes são assinados com sua chave ed25519 para atribuição de autoria na rede.

## Arquitetura

```
                +-------------------------+
                |     BuscaLogo Agent     |
                |       (binário Go)      |
                +-------------------------+
                   |      |        |
               CoreDNS  Yggdrasil  Scraper
                   |      |        |
                   +------+--------+
                          |
                 API HTTP  127.0.0.1:9970
                          |
              Painel web / janela Neutralino
                          |
                    Extensão do navegador
```

Dados e binários embutidos ficam no home do Agent (em geral `/opt/buscalogo` na instalação, ou `data/` em desenvolvimento).

## Configuração

Arquivo principal: `config.yaml` (no diretório de dados do Agent).

Padrões relevantes:

| Item | Padrão |
|------|--------|
| Painel / API | `127.0.0.1:9970` |
| CouchDB | `127.0.0.1:5984` |
| DNS | CoreDNS local (domínios `.bl`) |
| Scraper | ativo → CouchDB |

Flags:

```bash
buscalogo-agent            # com bandeja (quando disponível)
buscalogo-agent --no-tray  # headless (usado pelo Neutralino)
```

## Extensão do navegador

| Pasta | Destino |
|-------|---------|
| `extension/chrome/` | Chrome, Chromium, Edge, Brave (MV3) |
| `extension/firefox/` | Firefox 109+ (MV3) |

Lojas e empacotamento: [extension/README.md](extension/README.md) · [extension/store/STORE.md](extension/store/STORE.md)

Chrome Web Store: [BuscaLogo Agent](https://chromewebstore.google.com/detail/buscalogo-agent/gecmkbanhikgnhpcdibplcfndapclneh)

## Estrutura do projeto

```
cmd/agent/           Entrada do Agent
internal/            API, conta, scraper, CouchDB, P2P, DNS, Yggdrasil, updates…
frontend/            Painel web embutido (go:embed)
desktop/             Shell desktop Neutralino
extension/           Extensões do navegador
assets/              Binários (Yggdrasil, CoreDNS, CouchDB)
sites/               Exemplos de sites .bl
www/                 Assets estáticos de sites
dist/                Scripts e artefatos de empacotamento
```

## Desenvolvimento

```bash
make build
make test
make vet
make fmt
```

Releases em tags `v*` via [`.github/workflows/release.yml`](.github/workflows/release.yml).

## Repositórios relacionados

- **buscalogo.com** — frontend público de busca
- **bl-scraper-server** — referência de compatibilidade do scraper/API
- **server** — serviços backend do ecossistema BuscaLogo

## Observações

O BuscaLogo Agent faz parte do projeto BuscaLogo. Prefira listeners locais para API e CouchDB, salvo se quiser expor de propósito na mesh.

---

**Idiomas:** [English](README.md) · [Português](README.pt.md) · [Español](README.es.md) · [日本語](README.ja.md)
