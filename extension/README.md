# Extensão BuscaLogo Agent

Verifica se a página aberta está indexada no **BuscaLogo Agent** local (`http://127.0.0.1:9970`) e permite sugerir a indexação.

No Firefox, em sites `*.bl` a extensão usa o proxy same-origin `/__buscalogo_agent__/` (servido pelo Agent) para contornar o bloqueio de acesso a `127.0.0.1`.

## Pastas

| Pasta | Uso |
|-------|-----|
| `chrome/` | Chrome, Chromium, Edge, Brave (MV3) |
| `firefox/` | Firefox 109+ (MV3) |
| `store/` | Privacidade, listagens e zips para as lojas |

## Publicar nas lojas

Ver **[store/STORE.md](store/STORE.md)**. Resumo:

```bash
./extension/store/pack.sh
```

Não há coleta de dados remotos — só Agent local. O ponto sensível da revisão é o content script em páginas http/https (chip de status).

## Instalação local (sem loja)

**Chrome (recomendado):** [Chrome Web Store — BuscaLogo Agent](https://chromewebstore.google.com/detail/buscalogo-agent/gecmkbanhikgnhpcdibplcfndapclneh)

No painel do agente → aba **Scraper** → **Instalar na Chrome Web Store**, ou sideload:
`chrome://extensions` / `about:debugging` apontando para `chrome/` ou `firefox/`.

Ou: **Abrir Chrome (sideload)** no painel (perfil dedicado com `--load-extension`).
