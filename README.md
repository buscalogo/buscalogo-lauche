# BuscaLogo Agent (Launcher)

**Languages:** [English](README.md) · [Português](README.pt.md) · [Español](README.es.md) · [日本語](README.ja.md)

Local daemon for the [BuscaLogo](https://buscalogo.com) decentralized search network. One Go binary manages mesh networking, DNS for `.bl` sites, scraping, storage, P2P search, a web control panel, and optional desktop/browser integration.

## What it does

| Component | Role |
|-----------|------|
| **Yggdrasil** | Mesh overlay network (peers + IPv6) |
| **CoreDNS** | Resolves `.bl` (and related) domains locally |
| **CouchDB** | Stores scrapes, users, and config |
| **Scraper** | Native Go crawler; indexes pages into CouchDB |
| **P2P search** | Queries other Agents via signaling |
| **Account** | Local register/login; ed25519-signed scrapes |
| **Web panel** | Control UI at `http://127.0.0.1:9970` |
| **Desktop** | Neutralino shell + system tray |
| **Extension** | Chrome/Firefox status chip + suggest index |

## Requirements

- **Linux** amd64 (primary target; `.deb` for Ubuntu 22.04+ / Debian 12+)
- **Go** 1.26+ (to build from source)
- For desktop builds: Node.js + [`@neutralinojs/neu`](https://neutralino.js.org/)

## Quick start

### Install from `.deb`

```bash
sudo dpkg -i buscalogo-agent_*_amd64.deb
buscalogo-agent
```

Open the panel: [http://127.0.0.1:9970](http://127.0.0.1:9970)

### Build from source

```bash
# Optional: download Yggdrasil, CoreDNS, CouchDB binaries
make assets

make build          # → ./buscalogo-agent
make run            # build + run

# Portable tarball
make dist

# Full package (agent + Neutralino desktop + extensions)
make deb
```

### Desktop (Neutralino)

```bash
npm install -g @neutralinojs/neu
make desktop-run      # development
make desktop-build    # production bundle
```

See [desktop/buscalogo-desktop/README.md](desktop/buscalogo-desktop/README.md).

## First run

1. Start the Agent (or desktop app).
2. If no account exists → **create a local account**.
3. If an account exists but you are logged out → **login screen** (app stays locked until login).
4. Session is persisted under `data/identity/session.json` (survives Agent restarts).
5. Export a **backup JSON** (includes private key) from the Profile tab — keep it safe.

Scrapes are signed with your ed25519 key so peers can attribute authorship.

## Architecture

```
                +-------------------------+
                |     BuscaLogo Agent     |
                |       (Go binary)       |
                +-------------------------+
                   |      |        |
               CoreDNS  Yggdrasil  Scraper
                   |      |        |
                   +------+--------+
                          |
                 HTTP API  127.0.0.1:9970
                          |
              Web panel / Neutralino window
                          |
                    Browser extension
```

Data and bundled binaries live under the Agent home (typically `/opt/buscalogo` when installed, or a project `data/` tree in development).

## Configuration

Main file: `config.yaml` (created/updated under the Agent data directory).

Notable defaults:

| Setting | Default |
|---------|---------|
| Panel / API | `127.0.0.1:9970` |
| CouchDB | `127.0.0.1:5984` |
| DNS | local CoreDNS (`.bl` search domains) |
| Scraper | enabled → CouchDB |

Flags:

```bash
buscalogo-agent            # with system tray (when available)
buscalogo-agent --no-tray  # headless (used by Neutralino)
```

## Browser extension

| Folder | Target |
|--------|--------|
| `extension/chrome/` | Chrome, Chromium, Edge, Brave (MV3) |
| `extension/firefox/` | Firefox 109+ (MV3) |

Store listing and packaging: [extension/README.md](extension/README.md) · [extension/store/STORE.md](extension/store/STORE.md)

Chrome Web Store: [BuscaLogo Agent](https://chromewebstore.google.com/detail/buscalogo-agent/gecmkbanhikgnhpcdibplcfndapclneh)

## Project layout

```
cmd/agent/           Agent entrypoint
internal/            API, account, scraper, CouchDB, P2P, DNS, Yggdrasil, updates…
frontend/            Embedded web panel (go:embed)
desktop/             Neutralino desktop shell
extension/           Browser extensions
assets/              Bundled/downloaded binaries (Yggdrasil, CoreDNS, CouchDB)
sites/               Sample .bl site configs
www/                 Static site assets
dist/                Packaging scripts and artifacts
```

## Development

```bash
make build
make test
make vet
make fmt
```

Releases are tagged `v*` and built by [`.github/workflows/release.yml`](.github/workflows/release.yml).

## Related repos

- **buscalogo.com** — public search frontend
- **bl-scraper-server** — scraper/API compatibility reference
- **server** — backend services for the BuscaLogo ecosystem

## License / notes

BuscaLogo Agent is part of the BuscaLogo project. Prefer local-only listeners for API and CouchDB unless you intentionally expose them on the mesh.

---

**Languages:** [English](README.md) · [Português](README.pt.md) · [Español](README.es.md) · [日本語](README.ja.md)
