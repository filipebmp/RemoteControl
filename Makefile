# =============================================================================
#  RemoteControl — Makefile de build de produção
# =============================================================================
#  Cliente Windows otimizado para produção:
#     -ldflags "-s -w"   remove tabela de símbolos (-s) e dados de debug (-w)
#     -H=windowsgui      aplicação GUI: sem janela de consola (cmd)
#     -trimpath          remove caminhos absolutos do sistema de build
#
#  Pré-requisitos: Go >= 1.22.  Antes do primeiro build: `make tidy`.
#
#  Uso:
#     make tidy            # go mod tidy (descarrega deps, gera go.sum)
#     make                 # compila client, server e operator
#     make client          # cliente Windows (GUI, otimizado)
#     make server          # relay Linux (para a Hetzner)
#     make operator        # operador (host atual)
#     make VERSION=1.0.0    # injeta a versão
#     make clean
# =============================================================================

CMD_CLIENT   ?= ./client
CMD_SERVER   ?= ./server
CMD_OPERATOR ?= ./operator

BIN_DIR ?= ./bin
VERSION ?= dev
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

STRIP_FLAGS  := -s -w
VERSION_VARS := -X 'main.version=$(VERSION)' -X 'main.commit=$(COMMIT)' -X 'main.buildDate=$(DATE)'
LDFLAGS_PROD := $(STRIP_FLAGS) $(VERSION_VARS)

export CGO_ENABLED := 0

.PHONY: all client client-console server operator tidy verify clean help

all: client server operator ## Compila tudo

client: ## Cliente Windows 64-bit (GUI sem consola, otimizado)
	@echo ">> cliente (windows/amd64, GUI, strip)"
	@mkdir -p $(BIN_DIR)
	GOOS=windows GOARCH=amd64 go build -trimpath \
		-ldflags "$(LDFLAGS_PROD) -H=windowsgui" \
		-o $(BIN_DIR)/rc-client.exe $(CMD_CLIENT)
	@echo ">> gerado: $(BIN_DIR)/rc-client.exe"

client-console: ## Cliente Windows com consola visível (debug: mostra logs)
	@echo ">> cliente DEBUG (windows/amd64, com consola)"
	@mkdir -p $(BIN_DIR)
	GOOS=windows GOARCH=amd64 go build -trimpath \
		-ldflags "$(LDFLAGS_PROD)" \
		-o $(BIN_DIR)/rc-client-debug.exe $(CMD_CLIENT)

server: ## Relay para a Hetzner (linux/amd64, otimizado)
	@echo ">> server/relay (linux/amd64, strip)"
	@mkdir -p $(BIN_DIR)
	GOOS=linux GOARCH=amd64 go build -trimpath \
		-ldflags "$(LDFLAGS_PROD)" \
		-o $(BIN_DIR)/rc-server $(CMD_SERVER)

operator: ## Operador (SO/arquitetura do host atual)
	@echo ">> operador (host nativo, strip)"
	@mkdir -p $(BIN_DIR)
	go build -trimpath -ldflags "$(LDFLAGS_PROD)" \
		-o $(BIN_DIR)/rc-operator $(CMD_OPERATOR)

tidy: ## go mod tidy (necessário no primeiro build)
	go mod tidy

verify: ## go vet + testes
	go vet ./...
	go test ./...

clean: ## Remove artefactos
	rm -rf $(BIN_DIR)

help: ## Mostra esta ajuda
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'
