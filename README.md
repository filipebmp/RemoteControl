# RemoteControl

Aplicação de acesso remoto (estilo AnyDesk) escrita em Go, com relay WebSocket,
criptografia ponta-a-ponta (X25519 + AES-256-GCM) e cliente Windows com captura
de ecrã e injeção de inputs.

> **Uso autorizado.** Esta ferramenta destina-se a acesso remoto **consentido**
> (suporte técnico, administração dos próprios equipamentos). O cliente exige
> **autorização explícita do utilizador local** em cada sessão. Não a utilize
> para aceder a computadores sem permissão.

## Arquitetura

```
  Cliente (Windows)              Relay (Hetzner)              Operador
  ┌────────────────┐    wss     ┌──────────────┐    wss     ┌──────────────┐
  │ captura ecrã   │◄──────────►│ reencaminha  │◄──────────►│ vê o ecrã    │
  │ injeta inputs  │  cifrado   │  "às cegas"  │  cifrado   │ envia inputs │
  │ pede consent.  │            │ (não decifra)│            │              │
  └────────────────┘            └──────────────┘            └──────────────┘
         └──────────────── E2EE X25519 + AES-256-GCM ───────────────┘
```

- **server/** — relay de sinalização WebSocket. Agrupa pares por *sala* e
  reencaminha mensagens sem as decifrar.
- **crypto/** — handshake X25519, derivação HKDF e cifra AES-256-GCM. A senha
  pré-partilhada (PSK) autentica o handshake e protege contra um relay malicioso.
- **protocol/** — definição das mensagens (sinalização + conteúdo cifrado).
- **client/** — (Windows) captura de ecrã (GDI→JPEG), multi-monitor, injeção de
  rato/teclado (SendInput) e máquina de permissões com diálogo de consentimento.
- **operator/** — recebe frames, grava o último em disco e envia comandos de input.

## Estrutura

```
go.mod
server/main.go
crypto/crypto.go
protocol/protocol.go
client/screen.go        (captura + multi-monitor)
client/input.go         (rato/teclado)
client/main.go          (consentimento + streaming)
client/main_other.go    (stub não-Windows)
operator/main.go
Makefile  |  scripts/build.sh
deploy/nginx/remotecontrol.conf
```

## Compilação

```bash
make tidy            # descarrega dependências e gera go.sum (1ª vez)
make server          # bin/rc-server      (linux/amd64, para a Hetzner)
make operator        # bin/rc-operator    (host atual)
make client          # bin/rc-client.exe  (windows/amd64, GUI, -s -w -H=windowsgui)
make client-console  # variante de debug com consola visível
```

## Execução (exemplo)

1. **Relay** (Hetzner), atrás do Nginx (ver `deploy/nginx/remotecontrol.conf`):
   ```bash
   ./rc-server -listen :8080
   ```
2. **Cliente** (máquina Windows a ser assistida):
   ```bash
   rc-client.exe -server wss://relay.exemplo.com/ws -room SALA123 -psk SENHA-FORTE
   ```
   Surge um diálogo: **Sim** = controlo total, **Não** = só visualização,
   **Cancelar** = recusar.
3. **Operador**:
   ```bash
   ./rc-operator -server wss://relay.exemplo.com/ws -room SALA123 -psk SENHA-FORTE
   ```
   O último frame é gravado em `frame_latest.jpg`. Comandos no stdin: `move x y`,
   `clickat x y`, `scroll n`, `key 13`, `text olá`, `help`, `quit`.

> A `room` e a `psk` têm de coincidir nos dois lados e devem ser combinadas por
> um canal seguro separado. A `psk` é o que impede um relay comprometido de
> fazer MITM.

## Notas de segurança

- A PSK é misturada na derivação de chaves (HKDF salt) e liga o E2EE à
  identidade: sem ela, a decifragem falha mesmo que o relay troque as chaves.
- Nonces AES-GCM são baseados num contador por sentido (sem reutilização) com
  deteção de replay no recetor.
- O relay nunca tem acesso ao conteúdo (ecrã/inputs), apenas ao envelope de
  routing (sala).

## Limitações / próximos passos

- O operador grava frames em disco em vez de os mostrar numa janela (um viewer
  GUI em tempo real fica por fazer).
- `go.sum` não está incluído — corra `make tidy`.
- Captura via GDI; para alta taxa de frames considere a Desktop Duplication API.
