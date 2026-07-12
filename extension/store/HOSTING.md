# Auto-hospedagem Chrome em http://extensions.bl

Conforme [Auto-hospedagem para Linux](https://developer.chrome.com/docs/extensions/how-to/distribute/host-on-linux?hl=pt-br).

## Por que `CRX_REQUIRED_PROOF_MISSING`?

O Chrome **apaga** `.crx` autoassinados se não houver prova da Web Store.
Isso é esperado. Em 2022+ a instalação por link só funciona com
[políticas enterprise](https://blog.janestreet.com/chrome-extensions-finding-the-missing-proof/):

- `ExtensionInstallSources` — de onde pode instalar
- `ExtensionInstallAllowlist` — ID permitido
- (opcional) `ExtensionInstallForcelist` — instala sozinho via `updates.xml`

## Política (faça uma vez)

```bash
cd /caminho/para/buscalogo-lauche
sudo bash extension/store/install-chrome-policy.sh
```

Reinicie o Chrome → `chrome://policy` → volte a http://extensions.bl:4000/ → Instalar.

Arquivo gerado: `/etc/opt/chrome/policies/managed/buscalogo-extension.json`

### Force-install (sem clique)

Em `chrome-policy.json`, adicione:

```json
"ExtensionInstallForcelist": [
  "pfngpgblanlbmjmkegaiellacehnjooa;http://extensions.bl:4000/updates.xml"
]
```

Rode de novo o script de política e reinicie o Chrome.

## Alternativa sem .crx

`chrome://extensions` → Modo desenvolvedor → **Carregar sem compactação** → `extension/chrome`

Ou pelo Agent: botão que abre Chrome com `--load-extension=...`.

## Servir (http-server)

```bash
cd extension/store
npx http-server -p 4000 -a 127.0.0.1 --cors
```

| URL | Função |
|-----|--------|
| http://extensions.bl:4000/ | Página |
| http://extensions.bl:4000/chrome.crx | Pacote |
| http://extensions.bl:4000/updates.xml | Update |

## Empacotar de novo (mesma chave)

```bash
google-chrome --pack-extension="$(pwd)/extension/chrome" \
  --pack-extension-key="$(pwd)/extension/chrome.pem"
cp -f extension/chrome.crx extension/store/chrome.crx
chmod 644 extension/store/chrome.crx
```

Bump `version` no `manifest.json` **e** no `updates.xml`.

ID: `pfngpgblanlbmjmkegaiellacehnjooa`

**Nunca** sirva o `chrome.pem` nesta pasta.
