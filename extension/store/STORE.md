# Publicação nas lojas — BuscaLogo Agent

## Resposta curta

**Não precisa de nada extremo.** A extensão só fala com o Agent em `127.0.0.1:9970`.  
O que as lojas pedem com mais rigor é **justificar** o content script em todas as páginas http/https (mostrar o chip de status).

## O que já foi preparado

| Item | Status |
|------|--------|
| Permissões base: `tabs` + `storage` + `scripting` + localhost | OK |
| Alerta na página via `optional_host_permissions` (usuário liga/desliga) | OK |
| Sem content_scripts fixos no manifesto (evita host amplo na instalação) | OK |
| Versão `1.0.3` | OK |
| Política de privacidade (`store/PRIVACY.md`) | OK |
| Textos de listagem PT/EN | OK |
| Script de zip para upload | OK |
| ID Firefox `agent-extension@buscalogo.com` | OK |

## Gerar zips

```bash
cd buscalogo-lauche
./extension/store/pack.sh
```

Saída:

- `extension/store/dist/buscalogo-agent-chrome-1.0.1.zip` → Chrome Web Store / Edge Add-ons
- `extension/store/dist/buscalogo-agent-firefox-1.0.1.zip` → Firefox AMO

## Chrome Web Store

1. Conta em https://chrome.google.com/webstore/devconsole (taxa única ~US$5)
2. **Novo item** → upload do zip Chrome
3. Preencha com os textos de `listing-pt.md` / `listing-en.md`
4. **Privacy policy URL:** hospede `PRIVACY.md` (ex.: `https://buscalogo.com/extensao/privacidade`) e cole a URL
5. Justificativas de permissão (copiar):

**Single purpose**  
> Help the user see whether the current page is indexed by their local BuscaLogo Agent and optionally queue it for indexing.

**tabs**  
> Read the active tab URL to query the local agent and update the badge/popup.

**Host 127.0.0.1:9970 / localhost:9970**  
> Communicate only with the BuscaLogo Agent running on the user’s machine. No remote servers.

**Content scripts (all http/https)**  
> Show a small on-page status chip (“Indexed” / “Suggest to BuscaLogo”) on the pages the user visits. No page content is uploaded to the cloud.

6. Screenshots: 1280×800 ou 640×400 (mín. 1). Tire do popup + chip na página.
7. Envie para revisão (costuma levar alguns dias).

## Firefox Add-ons (AMO)

1. Conta em https://addons.mozilla.org/developers/
2. **Submit a New Add-on** → upload do zip Firefox
3. Mesmos textos + privacy URL
4. Source code: se pedirem, aponte para este repositório (`extension/`)
5. `data_collection_permissions: none` já está no manifest

## Edge Add-ons

Pode reutilizar o **mesmo zip Chrome** em https://partner.microsoft.com/dashboard (programa Edge).

## Checklist antes de enviar

- [ ] Agent local testado com a build empacotada
- [ ] Privacy policy publicada em URL https pública
- [ ] Screenshots prontos
- [ ] E-mail de contato válido na conta da loja
- [ ] Nome/ícone sem violar marcas de terceiros

## O que pode atrasar a revisão

1. Content script em todas as URLs — **esperado**; justifique bem (já acima)
2. Privacy policy ausente ou genérica demais
3. Descrição que promete busca na nuvem sem o Agent (deixe claro: **requer Agent local**)
4. Permissões extras desnecessárias — já removidas
