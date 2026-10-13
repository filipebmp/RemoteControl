# =============================================================================
#  RemoteControl — Makefile de build de produção
# =============================================================================
#  Compila a partir de qualquer SO (Linux, macOS, Windows) e gera os binários
#  de produção. O cliente Windows é otimizado:
#     -ldflags "-s -w"   -> remove tabela de símbolos (-s) e informação DWARF
#                           de debug (-w): binário mais pequeno e menos trivial
#                           de analisar.
#     -H=windowsgui      -> aplicação GUI: não abre janela de consola (cmd).
#
#  Pré-requisitos: Go >= 1.21 instalado.
#
#  Uso:
#     make                 # compila tudo (client, server, operator)
#     make client          # só o cliente Windows (GUI, otimizado)
#     make server          # só o relay (Linux amd64, para o Hetzner)
#     make operator        # só o operador (SO/arch do host atual)
#     make VERSION=1.2.0    # injeta a versão no binário
#     make clean
# =============================================================================

# ---- Configuração -----------------------------------------------------------
# Ajuste estes caminhos à estrutura real do seu projeto (cmd/<app>).
CMD_CLIENT   ?= ./cmd/client
CMD_SERVER   ?= ./cmd/server
CMD_OPERATOR ?= ./cmd/operator

BIN_DIR      ?= ./bin
VERSION      ?= dev
COMMIT       := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE         := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

# Flags de linker partilhadas: strip de símbolos/debug + metadados de versão.
# As variáveis main.version/commit/date são opcionais — remova o -X se o
# seu pacote main não as declarar.
STRIP_FLAGS  := -s -w
VERSION_VARS := -X 'main.version=$(VERSION)' \
                -X 'main.commit=$(COMMIT)' \
                -X 'main.buildDate=$(DATE)'

LDFLAGS_PROD := $(STRIP_FLAGS) $(VERSION_VARS)

# CGO desligado -> binários estáticos, cross-compile sem toolchain de C.
export CGO_ENABLED := 0

# ---- Alvos ------------------------------------------------------------------
.PHONY: all client server operator clean tidy verify help

all: client server operator ## Compila client, server e operator

client: ## Cliente Windows 64-bit (GUI sem consola, otimizado)
	@echo ">> A compilar cliente (windows/amd64, GUI, strip)..."
	@mkdir -p $(BIN_DIR)
	GOOS=windows GOARCH=amd64 \
		go build -trimpath \
			-ldflags "$(LDFLAGS_PROD) -H=windowsgui" \
			-o $(BIN_DIR)/rc-client.exe $(CMD_CLIENT)
	@echo ">> Gerado: $(BIN_DIR)/rc-client.exe"

server: ## Relay para o Hetzner (linux/amd64, otimizado)
	@echo ">> A compilar server/relay (linux/amd64, strip)..."
	@mkdir -p $(BIN_DIR)
	GOOS=linux GOARCH=amd64 \
		go build -trimpath \
			-ldflags "$(LDFLAGS_PROD)" \
			-o $(BIN_DIR)/rc-server $(CMD_SERVER)
	@echo ">> Gerado: $(BIN_DIR)/rc-server"

operator: ## Operador (SO/arquitetura do host atual)
	@echo ">> A compilar operador (host nativo, strip)..."
	@mkdir -p $(BIN_DIR)
	go build -trimpath \
		-ldflags "$(LDFLAGS_PROD)" \
		-o $(BIN_DIR)/rc-operator $(CMD_OPERATOR)
	@echo ">> Gerado: $(BIN_DIR)/rc-operator"

# Variante opcional do cliente que MANTÉM a consola — útil para debug,
# pois mostra logs e panics diretamente no terminal.
client-console: ## Cliente Windows com consola visível (debug)
	@echo ">> A compilar cliente DEBUG (windows/amd64, com consola)..."
	@mkdir -p $(BIN_DIR)
	GOOS=windows GOARCH=amd64 \
		go build -trimpath \
			-ldflags "$(LDFLAGS_PROD)" \
			-o $(BIN_DIR)/rc-client-debug.exe $(CMD_CLIENT)

tidy: ## go mod tidy
	go mod tidy

verify: ## vet + testes rápidos
	go vet ./...
	go test ./...

clean: ## Remove artefactos de build
	rm -rf $(BIN_DIR)

help: ## Mostra esta ajuda
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'
