// ui.go — viewer GUI do operador, servido como aplicação web local.
//
// Em vez de depender de um toolkit GUI nativo (que exigiria CGO/OpenGL), o
// operador expõe uma pequena página web em localhost. O browser:
//   - recebe os frames JPEG (binário via WebSocket) e mostra-os num <img>;
//   - captura rato (mover/clicar/scroll) e teclado e reenvia-os ao operador,
//     que os cifra e encaminha ao cliente.
//
// Toda a criptografia E2EE permanece no processo operador; o browser só fala
// com o localhost, nunca diretamente com o relay.
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/filipebmp/remotecontrol/protocol"
)

var localUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 1 << 16,
	CheckOrigin:     func(r *http.Request) bool { return true }, // só escuta em localhost
}

// localClient é uma ligação de browser, com escrita serializada.
type localClient struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (lc *localClient) writeBinary(b []byte) error {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	_ = lc.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return lc.conn.WriteMessage(websocket.BinaryMessage, b)
}

// UIServer serve o viewer e encaminha os inputs do browser para onInput.
type UIServer struct {
	onInput func(protocol.InputEvent)

	mu      sync.Mutex
	clients map[*localClient]bool
}

func NewUIServer(onInput func(protocol.InputEvent)) *UIServer {
	return &UIServer{
		onInput: onInput,
		clients: make(map[*localClient]bool),
	}
}

// Start arranca o servidor HTTP local numa goroutine.
func (u *UIServer) Start(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(viewerHTML))
	})
	mux.HandleFunc("/ws", u.handleWS)

	ln := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- ln.ListenAndServe() }()

	// Dá um instante ao servidor para falhar cedo (porta ocupada, etc.).
	select {
	case err := <-errc:
		return err
	case <-time.After(150 * time.Millisecond):
		return nil
	}
}

// PushFrame envia um frame JPEG a todos os browsers ligados.
func (u *UIServer) PushFrame(jpeg []byte) {
	u.mu.Lock()
	clients := make([]*localClient, 0, len(u.clients))
	for lc := range u.clients {
		clients = append(clients, lc)
	}
	u.mu.Unlock()

	for _, lc := range clients {
		if err := lc.writeBinary(jpeg); err != nil {
			u.mu.Lock()
			delete(u.clients, lc)
			u.mu.Unlock()
			_ = lc.conn.Close()
		}
	}
}

func (u *UIServer) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := localUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("upgrade do viewer falhou: %v", err)
		return
	}
	lc := &localClient{conn: conn}
	u.mu.Lock()
	u.clients[lc] = true
	u.mu.Unlock()
	log.Print("viewer ligado")

	defer func() {
		u.mu.Lock()
		delete(u.clients, lc)
		u.mu.Unlock()
		_ = conn.Close()
		log.Print("viewer desligado")
	}()

	// Lê eventos de input do browser (JSON) e reencaminha-os.
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var ev protocol.InputEvent
		if json.Unmarshal(raw, &ev) != nil {
			continue
		}
		if ev.Kind == "" {
			continue
		}
		u.onInput(ev)
	}
}

// viewerHTML é a página do viewer (sem dependências externas).
const viewerHTML = `<!doctype html>
<html lang="pt">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>RemoteControl — Viewer</title>
<style>
  :root { color-scheme: dark; }
  * { box-sizing: border-box; }
  body { margin: 0; background: #0b0d10; color: #e6e9ef;
         font: 14px/1.4 system-ui, -apple-system, Segoe UI, Roboto, sans-serif; }
  header { display: flex; align-items: center; gap: 12px; padding: 8px 14px;
           background: #14181d; border-bottom: 1px solid #222; position: sticky; top: 0; }
  header h1 { font-size: 14px; font-weight: 600; margin: 0; }
  .dot { width: 9px; height: 9px; border-radius: 50%; background: #c0392b; }
  .dot.on { background: #27ae60; }
  .hint { color: #8a93a2; font-size: 12px; margin-left: auto; }
  #wrap { display: flex; justify-content: center; padding: 12px; }
  #screen { max-width: 100%; height: auto; border: 1px solid #222; border-radius: 6px;
            cursor: crosshair; background: #000; outline: none; }
  #screen:focus { border-color: #3b82f6; }
</style>
</head>
<body>
<header>
  <span class="dot" id="dot"></span>
  <h1>RemoteControl — Viewer</h1>
  <span class="hint">clique na imagem para focar · rato e teclado são enviados ao computador remoto</span>
</header>
<div id="wrap">
  <img id="screen" tabindex="0" alt="A aguardar frames do computador remoto…">
</div>
<script>
(function () {
  const img = document.getElementById('screen');
  const dot = document.getElementById('dot');
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  let ws, lastURL = null;

  function connect() {
    ws = new WebSocket(proto + '://' + location.host + '/ws');
    ws.binaryType = 'arraybuffer';
    ws.onopen = () => dot.classList.add('on');
    ws.onclose = () => { dot.classList.remove('on'); setTimeout(connect, 1000); };
    ws.onmessage = (e) => {
      if (typeof e.data === 'string') return;
      const blob = new Blob([e.data], { type: 'image/jpeg' });
      const url = URL.createObjectURL(blob);
      img.src = url;
      if (lastURL) URL.revokeObjectURL(lastURL);
      lastURL = url;
    };
  }
  connect();

  function send(ev) {
    if (ws && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify(ev));
  }

  // Converte as coordenadas do clique (no elemento) para pixels do frame.
  function pos(e) {
    const r = img.getBoundingClientRect();
    if (!img.naturalWidth || r.width === 0) return { x: 0, y: 0 };
    return {
      x: Math.round((e.clientX - r.left) * img.naturalWidth / r.width),
      y: Math.round((e.clientY - r.top) * img.naturalHeight / r.height),
    };
  }

  function button(b) { return b === 2 ? 'right' : (b === 1 ? 'middle' : 'left'); }

  // Rato (throttle do movimento para ~30/s).
  let lastMove = 0;
  img.addEventListener('mousemove', (e) => {
    const now = Date.now();
    if (now - lastMove < 33) return;
    lastMove = now;
    const p = pos(e);
    send({ kind: 'move', x: p.x, y: p.y });
  });
  img.addEventListener('mousedown', (e) => {
    img.focus();
    const p = pos(e);
    send({ kind: 'click', x: p.x, y: p.y, button: button(e.button) });
    e.preventDefault();
  });
  img.addEventListener('contextmenu', (e) => e.preventDefault());
  img.addEventListener('wheel', (e) => {
    send({ kind: 'scroll', delta: e.deltaY < 0 ? 1 : -1 });
    e.preventDefault();
  }, { passive: false });

  // Teclado: teclas especiais por virtual-key; restantes como texto Unicode.
  const special = {
    Enter: 13, Backspace: 8, Tab: 9, Escape: 27, Delete: 46,
    ArrowLeft: 37, ArrowUp: 38, ArrowRight: 39, ArrowDown: 40,
    Home: 36, End: 35, PageUp: 33, PageDown: 34,
  };
  img.addEventListener('keydown', (e) => {
    if (special[e.key] !== undefined) {
      send({ kind: 'key', vk: special[e.key] });
      e.preventDefault();
    } else if (e.key.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey) {
      send({ kind: 'text', text: e.key });
      e.preventDefault();
    }
  });
})();
</script>
</body>
</html>`
