# Deploy do relay RemoteControl

Guia para pôr o **relay** (`rc-server`) a correr num servidor Linux (ex.: uma
VM Hetzner com Ubuntu/Debian), atrás de Nginx com TLS.

> O relay é **cego**: só reencaminha mensagens cifradas entre o cliente e o
> operador, agrupadas por *sala*. Não decifra nada. Mesmo assim, exponha-o
> sempre por HTTPS/WSS — o TLS protege os metadados de routing e o handshake.

## Arquitetura do que vamos montar

```
  Internet ──wss://relay.exemplo.com/443──►  Nginx (TLS)  ──http://127.0.0.1:8080──►  rc-server
                                              │                                        (systemd)
                                              └── certbot/Let's Encrypt
```

O `rc-server` escuta **apenas** em `127.0.0.1:8080`; só o Nginx lhe fala.

## Pré-requisitos

- Um servidor Linux com `systemd` (Ubuntu 22.04+/Debian 12+) e acesso `sudo`.
- Um domínio (ex.: `relay.exemplo.com`) com registo **A/AAAA** a apontar para
  o IP do servidor. É preciso para o certificado TLS.
- Portas **80** e **443** abertas no firewall/cloud.

---

## Passo 1 — Compilar o relay e enviá-lo para o servidor

No teu computador (ou nesta sessão, onde já está compilado em `bin/`):

```bash
make server          # gera bin/rc-server (linux/amd64)
```

Envia o binário e a pasta `deploy/` para o servidor:

```bash
scp bin/rc-server            utilizador@SERVIDOR:/tmp/rc-server
scp -r deploy                utilizador@SERVIDOR:/tmp/deploy
```

> Alternativa: `git clone` do repositório no servidor e `make server` lá
> (precisa do Go ≥ 1.22 instalado).

## Passo 2 — Instalar o serviço (systemd)

No **servidor**:

```bash
sudo /tmp/deploy/deploy.sh /tmp/rc-server
```

O script:
- cria o utilizador de sistema `rcrelay` (sem login);
- instala o binário em `/usr/local/bin/rc-server`;
- instala e arranca o serviço `rc-server.service` (a escutar em `127.0.0.1:8080`);
- confirma o `/healthz` localmente.

Verifica:

```bash
systemctl status rc-server
curl -fsS http://127.0.0.1:8080/healthz && echo OK
journalctl -u rc-server -f          # logs em tempo real
```

## Passo 3 — Nginx (reverse proxy + WebSocket)

Instala o Nginx e coloca a configuração (troca `relay.exemplo.com` pelo teu
domínio em **todas** as ocorrências):

```bash
sudo apt update && sudo apt install -y nginx
sudo sed -i 's/relay\.exemplo\.com/relay.TEUDOMINIO.com/g' \
     /tmp/deploy/nginx/remotecontrol.conf
sudo cp /tmp/deploy/nginx/remotecontrol.conf \
        /etc/nginx/sites-available/remotecontrol.conf
sudo ln -sf /etc/nginx/sites-available/remotecontrol.conf \
            /etc/nginx/sites-enabled/remotecontrol.conf
sudo rm -f /etc/nginx/sites-enabled/default   # opcional: remove o site default
sudo nginx -t
```

> O `nginx -t` vai falhar enquanto não existir o certificado (Passo 4) porque
> a config aponta para ficheiros `.pem` ainda inexistentes. É esperado — o
> certbot cria-os e recarrega o Nginx a seguir.

## Passo 4 — TLS com Let's Encrypt (certbot)

```bash
sudo apt install -y certbot python3-certbot-nginx
sudo certbot --nginx -d relay.TEUDOMINIO.com
```

O certbot obtém o certificado, preenche os caminhos no Nginx e recarrega.
A renovação automática fica agendada (confirma com `systemctl list-timers | grep certbot`).

```bash
sudo nginx -t && sudo systemctl reload nginx
```

## Passo 5 — Firewall

Com `ufw` (Ubuntu):

```bash
sudo ufw allow OpenSSH
sudo ufw allow 'Nginx Full'     # abre 80 e 443
sudo ufw enable
```

Numa Hetzner Cloud, abre também 80/443 na *Firewall* do painel. **Não** exponhas
o 8080 — ele é só local.

## Passo 6 — Testar de ponta a ponta

```bash
# healthcheck público (via Nginx/TLS):
curl -fsS https://relay.TEUDOMINIO.com/healthz && echo OK
```

Depois, numa sessão real:

```bash
# Máquina Windows a ser assistida (surge o diálogo de consentimento):
rc-client.exe -server wss://relay.TEUDOMINIO.com/ws -room SALA123 -psk SENHA-FORTE

# No teu PC (operador, com viewer web):
./rc-operator -server wss://relay.TEUDOMINIO.com/ws -room SALA123 -psk SENHA-FORTE -ui :9000
# abre http://localhost:9000
```

A `room` e a `psk` têm de coincidir nos dois lados e devem ser combinadas por um
canal seguro à parte. A `psk` é o que impede um relay comprometido de fazer MITM.

---

## Operação

| Ação | Comando |
|------|---------|
| Ver estado | `systemctl status rc-server` |
| Logs | `journalctl -u rc-server -f` |
| Reiniciar | `sudo systemctl restart rc-server` |
| Parar | `sudo systemctl stop rc-server` |
| Atualizar o binário | `scp` o novo `rc-server` e corre outra vez `sudo deploy/deploy.sh /tmp/rc-server` |

### Mudar a porta local

A unidade usa `-listen 127.0.0.1:8080`. Para mudar, edita o `ExecStart` em
`/etc/systemd/system/rc-server.service` (ou o ficheiro `deploy/systemd/rc-server.service`
antes de instalar) e o `upstream` em `remotecontrol.conf`, depois:
`sudo systemctl daemon-reload && sudo systemctl restart rc-server`.

## Desinstalar

```bash
sudo systemctl disable --now rc-server
sudo rm /etc/systemd/system/rc-server.service /usr/local/bin/rc-server
sudo systemctl daemon-reload
sudo userdel rcrelay
sudo rm -f /etc/nginx/sites-enabled/remotecontrol.conf \
           /etc/nginx/sites-available/remotecontrol.conf
sudo systemctl reload nginx
```

## Alternativa: Docker

Se preferires contentores em vez de systemd, vê `deploy/Dockerfile`:

```bash
docker build -t rc-server -f deploy/Dockerfile .
docker run -d --name rc-server -p 127.0.0.1:8080:8080 --restart unless-stopped rc-server
```

O Nginx + TLS (Passos 3–5) continuam iguais, a fazer proxy para `127.0.0.1:8080`.

## Notas de segurança

- Mantém o `8080` **só** em `127.0.0.1` — nunca exposto diretamente.
- O relay não guarda estado nem segredos; a confidencialidade vem do E2EE entre
  cliente e operador. Ainda assim, o TLS do Nginx é obrigatório em produção.
- Usa `psk` fortes e únicas por sessão/cliente. Trata a `room` como um
  identificador público (quem souber a `room` junta-se à sinalização, mas sem a
  `psk` não decifra nada).
