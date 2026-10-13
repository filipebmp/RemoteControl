// Package protocol define as mensagens trocadas entre cliente, operador e
// relay. Há dois níveis:
//
//   - Signal: mensagem de transporte (texto claro) encaminhada pelo relay.
//     O relay é "cego": só interpreta o "join" (para routing por sala) e
//     reencaminha tudo o resto sem perceber o conteúdo.
//   - Message: o conteúdo cifrado ponta-a-ponta (vai dentro de Signal.Data,
//     após ser decifrado com AES-256-GCM). O relay nunca vê este conteúdo.
package protocol

const (
	// Tipos de Signal (texto claro, nível de transporte).
	TypeJoin  = "join"  // primeiro pacote: regista o peer numa sala
	TypeHello = "hello" // troca da chave pública X25519 (handshake)
	TypeEnc   = "enc"   // payload cifrado (AES-256-GCM)
	TypeBye   = "bye"   // término gracioso

	// Papéis.
	RoleClient   = "client"   // máquina controlada (Windows)
	RoleOperator = "operator" // quem controla

	// Modos de permissão concedidos pelo utilizador local (no cliente).
	ModeDenied   = "denied"   // acesso recusado
	ModeReadOnly = "readonly" // só visualização
	ModeFull     = "full"     // visualização + rato/teclado

	// Tipos de Message (conteúdo cifrado).
	MsgPerm  = "perm"  // cliente informa o operador do modo concedido
	MsgFrame = "frame" // cliente -> operador: frame do ecrã
	MsgInput = "input" // operador -> cliente: evento de rato/teclado
)

// Signal é a unidade encaminhada pelo relay via WebSocket.
type Signal struct {
	Type  string `json:"type"`
	Room  string `json:"room,omitempty"`  // só no join
	Role  string `json:"role,omitempty"`  // só no join
	Pub   string `json:"pub,omitempty"`   // base64 da chave pública (hello)
	Nonce string `json:"nonce,omitempty"` // base64 do nonce GCM (enc)
	Data  string `json:"data,omitempty"`  // base64 do ciphertext (enc)
}

// Message é o conteúdo cifrado ponta-a-ponta.
type Message struct {
	Type string `json:"type"`

	// MsgPerm
	Mode string `json:"mode,omitempty"`

	// MsgFrame
	Monitor int    `json:"monitor,omitempty"`
	OriginX int    `json:"ox,omitempty"` // origem X do monitor no desktop virtual
	OriginY int    `json:"oy,omitempty"` // origem Y do monitor no desktop virtual
	Width   int    `json:"w,omitempty"`
	Height  int    `json:"h,omitempty"`
	JPEG    []byte `json:"jpeg,omitempty"` // imagem JPEG (base64 no JSON)

	// MsgInput
	Input *InputEvent `json:"input,omitempty"`
}

// InputEvent descreve um evento de rato/teclado enviado pelo operador.
// As coordenadas X/Y são relativas ao frame recebido (o cliente converte-as
// para coordenadas absolutas do ecrã, somando a origem do monitor).
type InputEvent struct {
	Kind   string `json:"kind"`             // move|click|scroll|key|text
	X      int    `json:"x,omitempty"`      // coordenada local ao frame
	Y      int    `json:"y,omitempty"`      // coordenada local ao frame
	Button string `json:"button,omitempty"` // left|right|middle (click)
	Delta  int    `json:"delta,omitempty"`  // nº de "cliques" de roda (scroll)
	VK     uint16 `json:"vk,omitempty"`     // virtual-key code (key)
	Text   string `json:"text,omitempty"`   // texto Unicode a digitar (text)
}
