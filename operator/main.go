// Comando operator — executa na máquina de quem controla.
//
// Responsabilidades:
//   - Ligar ao relay e concluir o handshake E2EE (X25519 + AES-256-GCM).
//   - Receber frames cifrados do cliente, decifrá-los e mostrá-los: num
//     viewer web (flag -ui) ou gravando o último em disco (modo stdin).
//   - Enviar eventos de rato/teclado cifrados (do viewer ou do stdin).
//
// É cross-platform (compila em Linux/macOS/Windows).
package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/filipebmp/remotecontrol/crypto"
	"github.com/filipebmp/remotecontrol/protocol"
)

type wsConn struct {
	c   *websocket.Conn
	wmu sync.Mutex
}

func (w *wsConn) writeSignal(s protocol.Signal) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	w.wmu.Lock()
	defer w.wmu.Unlock()
	return w.c.WriteMessage(websocket.TextMessage, b)
}

func main() {
	server := flag.String("server", "ws://localhost:8080/ws", "URL do relay (ws:// ou wss://)")
	room := flag.String("room", "", "código da sala/sessão (igual no cliente)")
	psk := flag.String("psk", "", "senha pré-partilhada da sessão (E2EE anti-MITM)")
	out := flag.String("out", "frame_latest.jpg", "ficheiro onde gravar o último frame (modo sem UI)")
	uiAddr := flag.String("ui", "", "endereço do viewer web local (ex.: :9000); vazio = modo stdin")
	flag.Parse()

	if *room == "" || *psk == "" {
		log.Fatal("--room e --psk são obrigatórios")
	}

	c, _, err := websocket.DefaultDialer.Dial(*server, nil)
	if err != nil {
		log.Fatalf("ligação ao relay falhou: %v", err)
	}
	defer c.Close()
	ws := &wsConn{c: c}

	if err := ws.writeSignal(protocol.Signal{
		Type: protocol.TypeJoin, Room: *room, Role: protocol.RoleOperator,
	}); err != nil {
		log.Fatalf("join falhou: %v", err)
	}

	priv, err := crypto.GenerateKeyPair()
	if err != nil {
		log.Fatal(err)
	}
	pubB64 := base64.StdEncoding.EncodeToString(crypto.PublicKeyBytes(priv))

	var (
		mu       sync.Mutex
		sess     *crypto.Session
		ui       *UIServer
		curMode  = protocol.ModeDenied
		derived  = make(chan struct{})
		done     = make(chan struct{})
		once     sync.Once
		doneOnce sync.Once
	)
	closeDerived := func() { once.Do(func() { close(derived) }) }
	closeDone := func() { doneOnce.Do(func() { close(done) }) }

	// Hello repetido até derivar (robusto à ordem de entrada).
	go func() {
		t := time.NewTicker(800 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-derived:
				return
			default:
			}
			_ = ws.writeSignal(protocol.Signal{Type: protocol.TypeHello, Pub: pubB64})
			<-t.C
		}
	}()

	sendEncrypted := func(m protocol.Message) error {
		mu.Lock()
		s := sess
		mu.Unlock()
		if s == nil {
			return fmt.Errorf("sessão ainda não estabelecida")
		}
		plain, err := json.Marshal(m)
		if err != nil {
			return err
		}
		nonce, ct := s.Seal(plain)
		return ws.writeSignal(protocol.Signal{
			Type:  protocol.TypeEnc,
			Nonce: base64.StdEncoding.EncodeToString(nonce),
			Data:  base64.StdEncoding.EncodeToString(ct),
		})
	}

	// Loop de leitura: handshake + receção de frames/permissões.
	go func() {
		defer closeDerived()
		defer closeDone()
		for {
			_, raw, err := c.ReadMessage()
			if err != nil {
				log.Printf("ligação terminada: %v", err)
				return
			}
			var sig protocol.Signal
			if json.Unmarshal(raw, &sig) != nil {
				continue
			}
			switch sig.Type {
			case protocol.TypeHello:
				mu.Lock()
				if sess == nil {
					if peerPub, derr := base64.StdEncoding.DecodeString(sig.Pub); derr == nil {
						if s, serr := crypto.DeriveSession(priv, peerPub, []byte(*psk), false); serr == nil {
							sess = s
							log.Print("sessão E2EE estabelecida")
							closeDerived()
							// Garante a entrega do nosso hello ao par já ligado.
							go helloBurst(ws, pubB64)
						} else {
							log.Printf("derivação de sessão falhou: %v", serr)
						}
					}
				}
				mu.Unlock()

			case protocol.TypeEnc:
				mu.Lock()
				s := sess
				mu.Unlock()
				if s == nil {
					continue
				}
				nonce, _ := base64.StdEncoding.DecodeString(sig.Nonce)
				ct, _ := base64.StdEncoding.DecodeString(sig.Data)
				plain, oerr := s.Open(nonce, ct)
				if oerr != nil {
					log.Printf("mensagem descartada: %v", oerr)
					continue
				}
				var m protocol.Message
				if json.Unmarshal(plain, &m) != nil {
					continue
				}
				switch m.Type {
				case protocol.MsgPerm:
					mu.Lock()
					curMode = m.Mode
					mu.Unlock()
					log.Printf(">> cliente concedeu modo: %s", m.Mode)
					if m.Mode == protocol.ModeDenied {
						log.Print(">> acesso recusado pelo utilizador remoto.")
					}
				case protocol.MsgFrame:
					mu.Lock()
					u := ui
					mu.Unlock()
					if u != nil {
						// Modo GUI: envia o frame ao browser em tempo real.
						u.PushFrame(m.JPEG)
					} else if werr := os.WriteFile(*out, m.JPEG, 0o644); werr != nil {
						log.Printf("gravar frame falhou: %v", werr)
					}
				}
			case protocol.TypeBye:
				log.Print(">> o cliente terminou a sessão.")
			}
		}
	}()

	// Aguarda handshake.
	select {
	case <-derived:
	case <-time.After(30 * time.Second):
		log.Fatal("tempo esgotado à espera do cliente / handshake")
	}
	mu.Lock()
	established := sess != nil
	mu.Unlock()
	if !established {
		log.Fatal("handshake não concluído (ligação caiu)")
	}

	// Modo GUI: inicia o viewer web local e transmite os frames ao browser.
	if *uiAddr != "" {
		u := NewUIServer(func(ev protocol.InputEvent) {
			if err := sendEncrypted(protocol.Message{Type: protocol.MsgInput, Input: &ev}); err != nil {
				log.Printf("envio de input falhou: %v", err)
			}
		})
		if err := u.Start(*uiAddr); err != nil {
			log.Fatalf("iniciar viewer web falhou: %v", err)
		}
		mu.Lock()
		ui = u
		mu.Unlock()
		log.Printf("viewer web pronto: abra http://localhost%s no browser", *uiAddr)
		<-done // bloqueia até a ligação ao relay terminar
		log.Print("ligação terminada; a sair.")
		return
	}

	// Modo stdin (sem UI): comandos em texto.
	printHelp(*out)
	commandLoop(sendEncrypted, func() string {
		mu.Lock()
		defer mu.Unlock()
		return curMode
	})
}

// commandLoop lê comandos do stdin e envia-os como eventos de input cifrados.
func commandLoop(send func(protocol.Message) error, mode func() string) {
	sc := bufio.NewScanner(os.Stdin)
	fmt.Print("> ")
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			fmt.Print("> ")
			continue
		}
		fields := strings.Fields(line)
		cmd := strings.ToLower(fields[0])

		if cmd == "quit" || cmd == "exit" {
			return
		}
		if cmd == "help" {
			printHelp("")
			fmt.Print("> ")
			continue
		}

		ev, err := parseCommand(cmd, fields, line)
		if err != nil {
			fmt.Printf("erro: %v\n> ", err)
			continue
		}
		if mode() != protocol.ModeFull {
			fmt.Printf("aviso: modo atual é %q — o cliente irá IGNORAR o input (apenas visualização)\n", mode())
		}
		if err := send(protocol.Message{Type: protocol.MsgInput, Input: ev}); err != nil {
			fmt.Printf("envio falhou: %v\n", err)
		}
		fmt.Print("> ")
	}
}

func parseCommand(cmd string, fields []string, raw string) (*protocol.InputEvent, error) {
	switch cmd {
	case "move":
		if len(fields) != 3 {
			return nil, fmt.Errorf("uso: move <x> <y>")
		}
		x, e1 := strconv.Atoi(fields[1])
		y, e2 := strconv.Atoi(fields[2])
		if e1 != nil || e2 != nil {
			return nil, fmt.Errorf("coordenadas inválidas")
		}
		return &protocol.InputEvent{Kind: "move", X: x, Y: y}, nil

	case "click":
		button := "left"
		if len(fields) >= 2 {
			button = strings.ToLower(fields[1])
		}
		return &protocol.InputEvent{Kind: "click", Button: button}, nil

	case "clickat":
		if len(fields) < 3 {
			return nil, fmt.Errorf("uso: clickat <x> <y> [left|right|middle]")
		}
		x, e1 := strconv.Atoi(fields[1])
		y, e2 := strconv.Atoi(fields[2])
		if e1 != nil || e2 != nil {
			return nil, fmt.Errorf("coordenadas inválidas")
		}
		button := "left"
		if len(fields) >= 4 {
			button = strings.ToLower(fields[3])
		}
		return &protocol.InputEvent{Kind: "click", X: x, Y: y, Button: button}, nil

	case "scroll":
		if len(fields) != 2 {
			return nil, fmt.Errorf("uso: scroll <delta> (ex.: 3 ou -3)")
		}
		d, err := strconv.Atoi(fields[1])
		if err != nil {
			return nil, fmt.Errorf("delta inválido")
		}
		return &protocol.InputEvent{Kind: "scroll", Delta: d}, nil

	case "key":
		if len(fields) != 2 {
			return nil, fmt.Errorf("uso: key <virtual-key-code decimal> (ex.: 13 = Enter)")
		}
		vk, err := strconv.Atoi(fields[1])
		if err != nil || vk < 0 || vk > 0xFFFF {
			return nil, fmt.Errorf("VK inválido")
		}
		return &protocol.InputEvent{Kind: "key", VK: uint16(vk)}, nil

	case "text":
		// tudo após "text " é tratado como literal
		txt := strings.TrimSpace(strings.TrimPrefix(raw, fields[0]))
		if txt == "" {
			return nil, fmt.Errorf("uso: text <texto a digitar>")
		}
		return &protocol.InputEvent{Kind: "text", Text: txt}, nil

	default:
		return nil, fmt.Errorf("comando desconhecido: %q (escreva 'help')", cmd)
	}
}

// helloBurst reenvia o hello algumas vezes para garantir a entrega ao par
// após a derivação da sessão (evita deadlock de handshake por perda).
func helloBurst(ws *wsConn, pubB64 string) {
	for i := 0; i < 6; i++ {
		_ = ws.writeSignal(protocol.Signal{Type: protocol.TypeHello, Pub: pubB64})
		time.Sleep(150 * time.Millisecond)
	}
}

func printHelp(out string) {
	fmt.Print(`
Comandos disponíveis:
  move <x> <y>                  mover o rato (coords locais ao frame)
  click [left|right|middle]     clicar na posição atual
  clickat <x> <y> [botão]       mover e clicar
  scroll <delta>                roda do rato (+cima / -baixo)
  key <vk>                      pressionar uma tecla por virtual-key (ex.: 13=Enter)
  text <texto>                  digitar texto
  help                          esta ajuda
  quit                          terminar
`)
	if out != "" {
		fmt.Printf("O último frame recebido é gravado em: %s\n", out)
	}
}
