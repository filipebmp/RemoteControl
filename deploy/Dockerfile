# =============================================================================
#  RemoteControl — imagem Docker do relay (alternativa ao systemd)
# =============================================================================
#  Build (a partir da raiz do repositório):
#     docker build -t rc-server -f deploy/Dockerfile .
#  Run (exposto só ao localhost; o Nginx faz o proxy + TLS):
#     docker run -d --name rc-server -p 127.0.0.1:8080:8080 \
#         --restart unless-stopped rc-server
# =============================================================================

# --- Etapa de build ---------------------------------------------------------
FROM golang:1.22-alpine AS build
WORKDIR /src

# Cache de dependências: copia primeiro os manifests.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w" \
    -o /out/rc-server ./server

# --- Etapa de runtime (imagem mínima) ---------------------------------------
FROM alpine:3.20
RUN apk add --no-cache wget ca-certificates \
    && adduser -S -D -H rcrelay
COPY --from=build /out/rc-server /usr/local/bin/rc-server

USER rcrelay
EXPOSE 8080

# Healthcheck via endpoint /healthz do próprio relay.
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null 2>&1 || exit 1

ENTRYPOINT ["/usr/local/bin/rc-server"]
# Dentro do contentor escuta em todas as interfaces; o mapeamento
# -p 127.0.0.1:8080:8080 no host é que o mantém privado.
CMD ["-listen", "0.0.0.0:8080"]
