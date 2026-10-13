//go:build windows

// Comando client — executa na máquina Windows a ser controlada.
//
// Responsabilidades:
//   - Ligar ao relay e estabelecer o canal E2EE (X25519 + AES-256-GCM).
//   - Pedir CONSENTIMENTO explícito ao utilizador local (MessageBox nativa),
//     que decide entre: controlo total / apenas visualização / recusar.
//   - Capturar o ecrã e transmitir frames JPEG cifrados.
//   - Aplicar inputs de rato/teclado recebidos — APENAS em modo "full".
//
// A máquina de estados de permissão é central: enquanto o utilizador não
// autorizar, nada é transmitido; em modo "readonly" os inputs são ignorados.
package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"log"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/gorilla/websocket"

	"github.com/filipebmp/remotecontrol/crypto"
	"github.com/filipebmp/remotecontrol/protocol"
)

var procMessageBoxW = user32.NewProc("MessageBoxW")

const (
	mbYesNoCancel   = 0x00000003
	mbIconQuestion  = 0x00000020
	mbSystemModal   = 0x00001000
	mbSetForeground = 0x00010000

	idYes = 6
	idNo  = 7
)

func messageBox(text, caption string, flags uint32) int {
	t, _ := syscall.UTF16PtrFromString(text)
	c, _ := syscall.UTF16PtrFromString(caption)
	r, _, _ := procMessageBoxW.Call(0,
		uintptr(unsafe.Pointer(t)),
		uintptr(unsafe.Pointer(c)),
		uintptr(flags),
	)
	return int(r)
}

// askConsent mostra o diálogo de autorização ao utilizador local e devolve
// o modo escolhido. Funciona mesmo em builds -H=windowsgui (sem consola).
func askConsent() string {
	ret := messageBox(
		"Um operador remoto está a pedir acesso a este computador.\n\n"+
			"SIM = Permitir CONTROLO TOTAL (ver ecrã + usar rato e teclado)\n"+
			"NÃO = Permitir APENAS VISUALIZAÇÃO (o operador só vê o ecrã)\n"+
			"CANCELAR = Recusar o acesso",
		"RemoteControl — Pedido de acesso remoto",
		mbYesNoCancel|mbIconQuestion|mbSystemModal|mbSetForeground,
	)
	switch ret {
	case idYes:
		return protocol.ModeFull
	case idNo:
		return protocol.ModeReadOnly
	default:
		return protocol.ModeDenied
	}
}

// wsConn serializa as escritas no WebSocket (exigido pelo gorilla).
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
	room := flag.String("room", "", "código da sala/sessão (igual no operador)")
	psk := flag.String("psk", "", "senha pré-partilhada da sessão (E2EE anti-MITM)")
	monIdx := flag.Int("monitor", -1, "índice do monitor a transmitir (-1 = desktop inteiro)")
	quality := flag.Int("quality", 60, "qualidade JPEG (1-100)")
	fps := flag.Int("fps", 8, "frames por segundo")
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
		Type: protocol.TypeJoin, Room: *room, Role: protocol.RoleClient,
	}); err != nil {
		log.Fatalf("join falhou: %v", err)
	}

	priv, err := crypto.GenerateKeyPair()
	if err != nil {
		log.Fatal(err)
	}
	pubB64 := base64.StdEncoding.EncodeToString(crypto.PublicKeyBytes(priv))

	var (
		mu      sync.Mutex
		sess    *crypto.Session
		mode    = protocol.ModeDenied
		region  Monitor
		derived = make(chan struct{})
		once    sync.Once
	)
	closeDerived := func() { once.Do(func() { close(derived) }) }

	// Envia "hello" repetidamente até derivar a sessão — torna o handshake
	// robusto à ordem de entrada dos pares e a perdas de mensagens.
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
			return nil
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

	// Loop de leitura: conclui o handshake e processa inputs cifrados.
	go func() {
		defer closeDerived() // desbloqueia o main se a ligação cair antes do handshake
		for {
			_, raw, err := c.ReadMessage()
			if err != nil {
				log.Printf("ligação terminada: %v", err)
				return
			}
			var sig protocol.Signal
			if err := json.Unmarshal(raw, &sig); err != nil {
				continue
			}
			switch sig.Type {
			case protocol.TypeHello:
				mu.Lock()
				if sess == nil {
					if peerPub, derr := base64.StdEncoding.DecodeString(sig.Pub); derr == nil {
						if s, serr := crypto.DeriveSession(priv, peerPub, []byte(*psk), true); serr == nil {
							sess = s
							log.Print("sessão E2EE estabelecida")
							closeDerived()
							// O par está comprovadamente ligado (o hello dele
							// chegou-nos). Garantimos que o NOSSO hello lhe chega,
							// mesmo que já tenhamos parado o envio periódico.
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
				curMode := mode
				curRegion := region
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
				if m.Type == protocol.MsgInput && m.Input != nil {
					handleInput(curMode, curRegion, m.Input)
				}
			}
		}
	}()

	// Aguarda a conclusão do handshake E2EE.
	select {
	case <-derived:
	case <-time.After(30 * time.Second):
		log.Fatal("tempo esgotado à espera do operador / handshake")
	}
	mu.Lock()
	established := sess != nil
	mu.Unlock()
	if !established {
		log.Fatal("handshake não concluído (ligação caiu)")
	}

	// --- Máquina de estados de permissão: pede consentimento ao utilizador ---
	chosen := askConsent()
	mu.Lock()
	mode = chosen
	mu.Unlock()
	log.Printf("modo autorizado pelo utilizador: %s", chosen)

	if err := sendEncrypted(protocol.Message{Type: protocol.MsgPerm, Mode: chosen}); err != nil {
		log.Printf("aviso: envio da permissão falhou: %v", err)
	}
	if chosen == protocol.ModeDenied {
		log.Print("acesso recusado pelo utilizador; a terminar.")
		_ = ws.writeSignal(protocol.Signal{Type: protocol.TypeBye})
		return
	}

	// Resolve a região a capturar (monitor específico ou desktop inteiro).
	reg := resolveRegion(*monIdx)
	mu.Lock()
	region = reg
	mu.Unlock()
	log.Printf("a transmitir: %s (%dx%d na origem %d,%d)", reg.Device, reg.W, reg.H, reg.X, reg.Y)

	// --- Loop de captura e streaming cifrado ---
	interval := time.Second / time.Duration(maxInt(1, *fps))
	for {
		jpg, cerr := CaptureJPEG(reg.X, reg.Y, reg.W, reg.H, *quality)
		if cerr != nil {
			log.Printf("captura falhou: %v", cerr)
			time.Sleep(interval)
			continue
		}
		if serr := sendEncrypted(protocol.Message{
			Type:    protocol.MsgFrame,
			Monitor: reg.Index,
			OriginX: reg.X, OriginY: reg.Y,
			Width: reg.W, Height: reg.H,
			JPEG: jpg,
		}); serr != nil {
			log.Printf("envio de frame falhou (ligação terminada?): %v", serr)
			return
		}
		time.Sleep(interval)
	}
}

func resolveRegion(monIdx int) Monitor {
	if monIdx < 0 {
		return VirtualScreen()
	}
	mons, err := ListMonitors()
	if err != nil || monIdx >= len(mons) {
		log.Printf("monitor %d indisponível; a usar o desktop inteiro", monIdx)
		return VirtualScreen()
	}
	return mons[monIdx]
}

// handleInput aplica um evento de input respeitando o modo de permissão.
// As coordenadas do evento são locais ao frame; somamos a origem do monitor
// para obter a posição absoluta no desktop.
func handleInput(mode string, region Monitor, ev *protocol.InputEvent) {
	if mode != protocol.ModeFull {
		return // Read-Only / Denied: nunca injeta inputs
	}
	switch ev.Kind {
	case "move":
		_ = MoveMouse(region.X+ev.X, region.Y+ev.Y)
	case "click":
		if ev.X != 0 || ev.Y != 0 {
			_ = MoveMouse(region.X+ev.X, region.Y+ev.Y)
		}
		_ = ClickButton(ev.Button)
	case "scroll":
		_ = ScrollWheel(ev.Delta)
	case "key":
		_ = KeyStroke(ev.VK)
	case "text":
		_ = TypeText(ev.Text)
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// helloBurst reenvia o hello algumas vezes para garantir a entrega ao par
// após a derivação da sessão (evita deadlock de handshake por perda).
func helloBurst(ws *wsConn, pubB64 string) {
	for i := 0; i < 6; i++ {
		_ = ws.writeSignal(protocol.Signal{Type: protocol.TypeHello, Pub: pubB64})
		time.Sleep(150 * time.Millisecond)
	}
}
