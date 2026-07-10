# Política de Privacidade — BuscaLogo Agent (extensão)

**Última atualização:** 10 de julho de 2026  
**Produto:** extensão de navegador “BuscaLogo Agent”  
**Contato:** contato@buscalogo.com  
**Site:** https://buscalogo.com

## Resumo

A extensão **não coleta dados pessoais** e **não envia informações para servidores da BuscaLogo na nuvem**. Toda a comunicação ocorre apenas com o **BuscaLogo Agent instalado na sua máquina** (`http://127.0.0.1:9970` ou `http://localhost:9970`).

## Quais dados a extensão usa

| Dado | Uso | Destino |
|------|-----|---------|
| URL da aba ativa | Verificar se a página já está indexada e, se você pedir, sugerir indexação | Somente o Agent local |
| Título da página (se já indexada) | Exibir status no popup/chip | Somente leitura da resposta local |

A extensão **não** lê o conteúdo HTML da página para enviar a terceiros. O raspagem/indexação, quando solicitada, é feita pelo Agent local.

## O que a extensão não faz

- Não vende nem compartilha dados com anunciantes
- Não usa analytics de terceiros
- Não faz login em contas Google/Mozilla
- Não armazena histórico de navegação em servidores remotos
- Não acessa a internet pública em nome do usuário (apenas o loopback local)

## Permissões

- **tabs** — obter a URL da aba para consultar o Agent local
- **http://127.0.0.1:9970** / **localhost:9970** — API do Agent na sua máquina
- **Content script em páginas http/https** — mostrar um aviso discreto (“Indexada” / “Sugerir”)

## Armazenamento local

A extensão não usa `chrome.storage` / IndexedDB para dados pessoais. Badges e UI são efêmeros.

## Crianças

O produto não é direcionado a menores de 13 anos e não coleta dados de crianças de forma intencional.

## Alterações

Mudanças relevantes nesta política serão refletidas nesta página com nova data de atualização.

## Contato

Dúvidas sobre privacidade: **contato@buscalogo.com**
