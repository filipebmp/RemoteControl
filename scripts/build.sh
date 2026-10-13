#!/usr/bin/env bash
# =============================================================================
#  RemoteControl — build de produção (alternativa ao Makefile)
# =============================================================================
#  Compila a partir de qualquer SO. Binário Windows otimizado:
#     -s -w         remove tabela de símbolos e dados de debug (DWARF)
#     -H=windowsgui aplicação GUI sem janela de consola (cmd)
#
#  Uso:
#     ./scripts/build.sh                 # compila tudo
#     ./scripts/build.sh client          # só o cliente Windows
#     ./scripts/build.sh server          # só o relay (linux/amd64)
#     VERSION=1.2.0 ./scripts/build.sh   # injeta versão
# =============================================================================
set -euo pipefail

CMD_CLIENT="${CMD_CLIENT:-./cmd/client}"
CMD_SERVER="${CMD_SERVER:-./cmd/server}"
CMD_OPERATOR="${CMD_OPERATOR:-./cmd/operator}"
BIN_DIR="${BIN_DIR:-./bin}"

VERSION="${VERSION:-dev}"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo none)"
DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

VERSION_VARS="-X 'main.version=${VERSION}' -X 'main.commit=${COMMIT}' -X 'main.buildDate=${DATE}'"
LDFLAGS_PROD="-s -w ${VERSION_VARS}"

export CGO_ENABLED=0
mkdir -p "${BIN_DIR}"

build_client() {
  echo ">> cliente (windows/amd64, GUI, strip)"
  GOOS=windows GOARCH=amd64 go build -trimpath \
    -ldflags "${LDFLAGS_PROD} -H=windowsgui" \
    -o "${BIN_DIR}/rc-client.exe" "${CMD_CLIENT}"
}

build_server() {
  echo ">> server/relay (linux/amd64, strip)"
  GOOS=linux GOARCH=amd64 go build -trimpath \
    -ldflags "${LDFLAGS_PROD}" \
    -o "${BIN_DIR}/rc-server" "${CMD_SERVER}"
}

build_operator() {
  echo ">> operador (host nativo, strip)"
  go build -trimpath \
    -ldflags "${LDFLAGS_PROD}" \
    -o "${BIN_DIR}/rc-operator" "${CMD_OPERATOR}"
}

case "${1:-all}" in
  client)   build_client ;;
  server)   build_server ;;
  operator) build_operator ;;
  all)      build_client; build_server; build_operator ;;
  *) echo "alvo desconhecido: $1 (use: client|server|operator|all)"; exit 1 ;;
esac

echo ">> concluído. Artefactos em ${BIN_DIR}/"
