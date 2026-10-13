// Comando server — o relay de sinalização WebSocket (alojado na Hetzner).
//
// Função: encaminhar mensagens "às cegas" entre os pares de uma mesma sala.
// O relay NÃO decifra nada — vê apenas o "join" (para saber a que sala
// pertence cada ligação) e reencaminha todo o resto tal como recebido.
// A confidencialidade é garantida pelo E2EE (pacote crypto) entre cliente
// e operador, pelo que um relay comprometido não consegue ler o tráfego.
package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/filipebmp/remotecontrol/protocol"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1 << 16,
	WriteBufferSize: 1 << 16,
	// Numa implantação real, valide a Origin. O cliente/operador nativos não
	// enviam Origin de browser, por isso aceitamos todas aqui.
	CheckOrigin: func(r *http.Request) bool { return true },
}

const (
	readLimit    = 16 << 20 // 16 MiB por mensagem (frames JPEG grandes)
	readTimeout  = 90 * time.Second
	pingInterval = 30 * time.Second
	sendBuffer   = 256
)

// peer representa uma ligação WebSocket registada numa sala.
type peer struct {
	conn *websocket.Conn
	send chan []byte
	room string
	mu   sync.Mutex // serializa escritas no conn (requisito do gorilla/websocket)
}

// hub mantém o mapeamento sala -> pares.
type hub struct {
	mu    sync.RWMutex
	rooms map[string]map[*peer]bool
}

func newHub() *hub { return &hub{rooms: make(map[string]map[*peer]bool)} }

func (h *hub) add(p *peer) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.rooms[p.room] == nil {
		h.rooms[p.room] = make(map[*peer]bool)
	}
	h.rooms[p.room][p] = true
}

func (h *hub) remove(p *peer) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if m := h.rooms[p.room]; m != nil {
		delete(m, p)
		if len(m) == 0 {
			delete(h.rooms, p.room)
		}
	}
}

// broadcast reencaminha msg para todos os pares da sala, exceto o remetente.
func (h *hub) broadcast(from *peer, msg []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for p := range h.rooms[from.room] {
		if p == from {
			continue
		}
		// Cópia: o buffer de leitura do gorilla é reutilizado após o retorno.
		cp := make([]byte, len(msg))
		copy(cp, msg)
		select {
		case p.send <- cp:
		default:
			// Buffer cheio (par lento): descarta para não bloquear o hub.
			log.Printf("sala=%q: buffer cheio, frame descartado", from.room)
		}
	}
}

func (h *hub) serveWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("upgrade falhou: %v", err)
		return
	}
	conn.SetReadLimit(readLimit)
	_ = conn.SetReadDeadline(time.Now().Add(readTimeout))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(readTimeout))
	})

	// A primeira mensagem TEM de ser um "join" com a sala.
	_, raw, err := conn.ReadMessage()
	if err != nil {
		_ = conn.Close()
		return
	}
	var join protocol.Signal
	if err := json.Unmarshal(raw, &join); err != nil ||
		join.Type != protocol.TypeJoin || join.Room == "" {
		log.Printf("join inválido; a fechar ligação")
		_ = conn.Close()
		return
	}

	p := &peer{conn: conn, send: make(chan []byte, sendBuffer), room: join.Room}
	h.add(p)
	log.Printf("peer entrou: sala=%q role=%q", join.Room, join.Role)

	defer func() {
		h.remove(p)
		_ = conn.Close()
		log.Printf("peer saiu: sala=%q", join.Room)
	}()

	go p.writePump()

	// Read pump: reencaminha tudo o que chegar para o(s) outro(s) par(es).
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		h.broadcast(p, msg)
	}
}

// writePump serializa as escritas e envia pings periódicos de keep-alive.
func (p *peer) writePump() {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	for {
		select {
		case msg, ok := <-p.send:
			if !ok {
				return
			}
			p.mu.Lock()
			err := p.conn.WriteMessage(websocket.TextMessage, msg)
			p.mu.Unlock()
			if err != nil {
				return
			}
		case <-ticker.C:
			p.mu.Lock()
			err := p.conn.WriteMessage(websocket.PingMessage, nil)
			p.mu.Unlock()
			if err != nil {
				return
			}
		}
	}
}

func main() {
	addr := flag.String("listen", ":8080", "endereço de escuta (ex.: :8080)")
	flag.Parse()

	h := newHub()
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", h.serveWS)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	srv := &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("relay a escutar em %s (WebSocket em /ws)", *addr)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("servidor terminou: %v", err)
	}
}
