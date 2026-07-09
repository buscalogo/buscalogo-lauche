#!/usr/bin/env bash
# Publica uma nova versão do BuscaLogo Agent no GitHub Releases.
# Uso: ./scripts/release.sh [patch|minor|major|X.Y.Z]
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

VERSION_FILE="$ROOT/VERSION"
die() { echo ">> Erro: $*" >&2; exit 1; }

command -v git >/dev/null || die "git não encontrado"

if [[ ! -f "$VERSION_FILE" ]]; then
  echo "0.1.0" > "$VERSION_FILE"
fi

CUR="$(tr -d ' \n\r' < "$VERSION_FILE")"
[[ "$CUR" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "VERSION inválida: $CUR"

bump() {
  local kind="${1:-patch}"
  IFS=. read -r major minor patch <<< "$CUR"
  case "$kind" in
    major) major=$((major + 1)); minor=0; patch=0 ;;
    minor) minor=$((minor + 1)); patch=0 ;;
    patch) patch=$((patch + 1)) ;;
    *) die "tipo inválido: $kind (use patch, minor, major ou X.Y.Z)" ;;
  esac
  echo "${major}.${minor}.${patch}"
}

ARG="${1:-patch}"
if [[ "$ARG" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  NEW="$ARG"
else
  NEW="$(bump "$ARG")"
fi

TAG="v${NEW}"

echo ">> Versão atual: $CUR"
echo ">> Nova versão:  $NEW ($TAG)"

# Bloqueia binários acidentais no commit
if git ls-files --error-unmatch agent buscalogo-agent 2>/dev/null; then
  die "binário 'agent' ou 'buscalogo-agent' está no git — remova antes do release"
fi
for bin in agent buscalogo-agent; do
  if [[ -f "$bin" ]] && git check-ignore -q "$bin" 2>/dev/null; then
    echo ">> Removendo binário local ignorado: $bin"
    rm -f "$bin"
  fi
done
if [[ -f agent ]] && [[ "$(stat -c%s agent 2>/dev/null || echo 0)" -gt 1000000 ]]; then
  die "arquivo local 'agent' ($(du -h agent | cut -f1)) — rode: rm -f agent"
fi

if ! git diff --quiet || ! git diff --cached --quiet; then
  echo ">> Aviso: há mudanças não commitadas no working tree"
  git status --short
  read -r -p ">> Continuar mesmo assim? [y/N] " ans
  [[ "${ans:-}" =~ ^[yY]$ ]] || die "abortado"
fi

if git rev-parse "$TAG" >/dev/null 2>&1; then
  die "tag $TAG já existe localmente"
fi

if git ls-remote --exit-code --tags origin "$TAG" >/dev/null 2>&1; then
  die "tag $TAG já existe no remoto"
fi

echo "$NEW" > "$VERSION_FILE"
git add VERSION
git commit -m "$(cat <<EOF
Release ${NEW}.

Bump VERSION for GitHub Actions to build and publish buscalogo-agent_${NEW}_amd64.deb.
EOF
)"

git tag -a "$TAG" -m "BuscaLogo Agent ${NEW}"

echo ""
echo ">> Commit e tag criados."
echo ">> Para publicar:"
echo "   git push origin main"
echo "   git push origin ${TAG}"
echo ""
read -r -p ">> Enviar agora para origin? [y/N] " push_now
if [[ "${push_now:-}" =~ ^[yY]$ ]]; then
  git push origin main
  git push origin "$TAG"
  echo ">> Publicado. Acompanhe: https://github.com/buscalogo/buscalogo-lauche/actions"
else
  echo ">> OK — rode manualmente quando quiser."
fi
