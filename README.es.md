# BuscaLogo Agent (Launcher)

**Idiomas:** [English](README.md) · [Português](README.pt.md) · [Español](README.es.md) · [日本語](README.ja.md)

Daemon local de la red de búsqueda descentralizada [BuscaLogo](https://buscalogo.com). Un solo binario en Go gestiona la mesh, DNS de sitios `.bl`, scraping, almacenamiento, búsqueda P2P, panel web e integración opcional con escritorio/navegador.

## Qué hace

| Componente | Función |
|------------|---------|
| **Yggdrasil** | Red mesh overlay (peers + IPv6) |
| **CoreDNS** | Resuelve dominios `.bl` (y afines) en local |
| **CouchDB** | Almacena scrapes, usuarios y configuración |
| **Scraper** | Crawler nativo en Go; indexa páginas en CouchDB |
| **Búsqueda P2P** | Consulta otros Agents vía signaling |
| **Cuenta** | Registro/login local; scrapes firmados con ed25519 |
| **Panel web** | UI de control en `http://127.0.0.1:9970` |
| **Escritorio** | Shell Neutralino + bandeja del sistema |
| **Extensión** | Chip de estado en Chrome/Firefox + sugerir indexación |

## Requisitos

- **Linux** amd64 (objetivo principal; `.deb` para Ubuntu 22.04+ / Debian 12+)
- **Go** 1.26+ (para compilar)
- Para escritorio: Node.js + [`@neutralinojs/neu`](https://neutralino.js.org/)

## Inicio rápido

### Instalar con `.deb`

```bash
sudo dpkg -i buscalogo-agent_*_amd64.deb
buscalogo-agent
```

Abre el panel: [http://127.0.0.1:9970](http://127.0.0.1:9970)

### Compilar desde el código

```bash
# Opcional: descargar binarios Yggdrasil, CoreDNS, CouchDB
make assets

make build          # → ./buscalogo-agent
make run            # compila y ejecuta

# Tarball portable
make dist

# Paquete completo (agent + escritorio Neutralino + extensiones)
make deb
```

### Escritorio (Neutralino)

```bash
npm install -g @neutralinojs/neu
make desktop-run      # desarrollo
make desktop-build    # bundle de producción
```

Ver [desktop/buscalogo-desktop/README.md](desktop/buscalogo-desktop/README.md).

## Primer uso

1. Inicia el Agent (o la app de escritorio).
2. Sin cuenta → pantalla de **registro**.
3. Con cuenta, sin sesión → pantalla de **login** (la app permanece bloqueada hasta autenticarse).
4. La sesión se guarda en `data/identity/session.json` (sobrevive reinicios).
5. Exporta el **backup JSON** (incluye clave privada) en la pestaña Perfil — guárdalo en un lugar seguro.

Los scrapes se firman con tu clave ed25519 para atribuir autoría en la red.

## Arquitectura

```
                +-------------------------+
                |     BuscaLogo Agent     |
                |       (binario Go)      |
                +-------------------------+
                   |      |        |
               CoreDNS  Yggdrasil  Scraper
                   |      |        |
                   +------+--------+
                          |
                 API HTTP  127.0.0.1:9970
                          |
              Panel web / ventana Neutralino
                          |
                    Extensión del navegador
```

Datos y binarios embebidos viven en el home del Agent (normalmente `/opt/buscalogo` al instalar, o `data/` en desarrollo).

## Configuración

Archivo principal: `config.yaml` (en el directorio de datos del Agent).

Valores por defecto relevantes:

| Ítem | Por defecto |
|------|-------------|
| Panel / API | `127.0.0.1:9970` |
| CouchDB | `127.0.0.1:5984` |
| DNS | CoreDNS local (dominios `.bl`) |
| Scraper | activo → CouchDB |

Flags:

```bash
buscalogo-agent            # con bandeja (si está disponible)
buscalogo-agent --no-tray  # headless (usado por Neutralino)
```

## Extensión del navegador

| Carpeta | Destino |
|---------|---------|
| `extension/chrome/` | Chrome, Chromium, Edge, Brave (MV3) |
| `extension/firefox/` | Firefox 109+ (MV3) |

Tiendas y empaquetado: [extension/README.md](extension/README.md) · [extension/store/STORE.md](extension/store/STORE.md)

Chrome Web Store: [BuscaLogo Agent](https://chromewebstore.google.com/detail/buscalogo-agent/gecmkbanhikgnhpcdibplcfndapclneh)

## Estructura del proyecto

```
cmd/agent/           Entrada del Agent
internal/            API, cuenta, scraper, CouchDB, P2P, DNS, Yggdrasil, updates…
frontend/            Panel web embebido (go:embed)
desktop/             Shell de escritorio Neutralino
extension/           Extensiones del navegador
assets/              Binarios (Yggdrasil, CoreDNS, CouchDB)
sites/               Ejemplos de sitios .bl
www/                 Assets estáticos de sitios
dist/                Scripts y artefactos de empaquetado
```

## Desarrollo

```bash
make build
make test
make vet
make fmt
```

Releases en tags `v*` vía [`.github/workflows/release.yml`](.github/workflows/release.yml).

## Repositorios relacionados

- **buscalogo.com** — frontend público de búsqueda
- **bl-scraper-server** — referencia de compatibilidad scraper/API
- **server** — servicios backend del ecosistema BuscaLogo

## Notas

BuscaLogo Agent forma parte del proyecto BuscaLogo. Prefiere listeners locales para API y CouchDB, salvo que quieras exponerlos a propósito en la mesh.

---

**Idiomas:** [English](README.md) · [Português](README.pt.md) · [Español](README.es.md) · [日本語](README.ja.md)
