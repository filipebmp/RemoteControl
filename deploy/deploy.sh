#!/usr/bin/env bash
# =============================================================================
#  RemoteControl — instalador do relay (corre NO SERVIDOR, com sudo)
# =============================================================================
#  Instala o binário do relay como serviço systemd confinado, a escutar em
#  127.0.0.1:8080. NÃO configura o Nginx nem o TLS — para isso siga o
#  deploy/README.md (certbot + remotecontrol.conf).
#
#  Uso (no servidor, depois de copiar o binário rc-server para lá):
#     sudo ./deploy/deploy.sh /caminho/para/rc-server
#
#  Se omitir o caminho, procura ./bin/rc-server e ./rc-server.
#
#  Idempotente: pode voltar a correr para atualizar o binário (faz restart).
# =============================================================================
set -euo pipefail

SERVICE_USER="rcrelay"
BIN_DEST="/usr/local/bin/rc-server"
UNIT_SRC_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/systemd"
UNIT_DEST="/etc/systemd/system/rc-server.service"

# --- 1. Localizar o binário a instalar --------------------------------------
BIN_SRC="${1:-}"
if [[ -z "${BIN_SRC}" ]]; then
  for c in ./bin/rc-server ./rc-server; do
    [[ -f "$c" ]] && BIN_SRC="$c" && break
  done
fi
if [[ -z "${BIN_SRC}" || ! -f "${BIN_SRC}" ]]; then
  echo "ERRO: binário do relay não encontrado." >&2
  echo "      Passe o caminho: sudo ./deploy/deploy.sh /caminho/para/rc-server" >&2
  exit 1
fi

if [[ "${EUID}" -ne 0 ]]; then
  echo "ERRO: corra com sudo (precisa de criar utilizador e instalar o serviço)." >&2
  exit 1
fi

echo ">> binário de origem: ${BIN_SRC}"

# --- 2. Utilizador de sistema dedicado (sem login, sem home) ----------------
if ! id "${SERVICE_USER}" &>/dev/null; then
  echo ">> a criar utilizador de sistema '${SERVICE_USER}'"
  useradd --system --no-create-home --shell /usr/sbin/nologin "${SERVICE_USER}"
else
  echo ">> utilizador '${SERVICE_USER}' já existe"
fi

# --- 3. Instalar o binário ---------------------------------------------------
echo ">> a instalar o binário em ${BIN_DEST}"
install -m 0755 -o root -g root "${BIN_SRC}" "${BIN_DEST}"

# --- 4. Instalar a unidade systemd ------------------------------------------
echo ">> a instalar a unidade systemd em ${UNIT_DEST}"
install -m 0644 -o root -g root "${UNIT_SRC_DIR}/rc-server.service" "${UNIT_DEST}"

# --- 5. Arrancar / reiniciar o serviço --------------------------------------
echo ">> a recarregar o systemd e a (re)iniciar o serviço"
systemctl daemon-reload
systemctl enable rc-server.service
systemctl restart rc-server.service

sleep 1
echo
echo "=============================================================="
systemctl --no-pager --full status rc-server.service || true
echo "=============================================================="
echo
echo ">> verificação local do healthcheck:"
if command -v curl &>/dev/null; then
  curl -fsS http://127.0.0.1:8080/healthz && echo " <- OK" || echo "  (relay ainda não respondeu; veja 'journalctl -u rc-server -f')"
fi
echo
echo ">> PRÓXIMO PASSO: configurar o Nginx + TLS (deploy/README.md)."
