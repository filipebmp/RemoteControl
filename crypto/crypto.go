// Package crypto implementa a criptografia ponta-a-ponta (E2EE) do sistema.
//
// Desenho:
//   - Troca de chaves: X25519 (Diffie-Hellman em curva elíptica). Cada lado
//     gera um par efémero e troca a chave pública via relay.
//   - Autenticação: uma senha pré-partilhada (PSK) conhecida fora-de-banda
//     é misturada na derivação (como salt do HKDF). Sem a PSK, um relay
//     malicioso que troque as chaves públicas (MITM) não consegue derivar as
//     chaves de sessão — a decifragem falha. Isto liga o E2EE à identidade.
//   - Derivação: HKDF-SHA256 produz DUAS chaves AES-256, uma por sentido
//     (cliente->operador e operador->cliente), ligadas ao "transcript"
//     (ambas as chaves públicas), evitando reutilização entre sentidos.
//   - Cifra: AES-256-GCM. O nonce de 96 bits é derivado de um contador por
//     sentido, eliminando o risco de reutilização de nonce e permitindo
//     deteção de replay no recetor.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
)

const (
	infoC2O = "remotecontrol/v1 key client->operator"
	infoO2C = "remotecontrol/v1 key operator->client"

	keySize   = 32 // AES-256
	nonceSize = 12 // GCM padrão
)

// GenerateKeyPair cria um par de chaves efémero X25519.
func GenerateKeyPair() (*ecdh.PrivateKey, error) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("gerar par de chaves X25519: %w", err)
	}
	return priv, nil
}

// PublicKeyBytes devolve a representação em bytes (32) da chave pública.
func PublicKeyBytes(priv *ecdh.PrivateKey) []byte {
	return priv.PublicKey().Bytes()
}

// Session guarda o estado de cifra de uma sessão E2EE estabelecida.
type Session struct {
	send     cipher.AEAD
	recv     cipher.AEAD
	sendCtr  uint64
	recvCtr  uint64
	recvSeen bool
}

// DeriveSession conclui o handshake: a partir da nossa chave privada, da
// chave pública do par e da PSK, deriva as chaves de sessão.
//
// isClient seleciona que chave é usada para enviar vs. receber, para que
// ambos os lados fiquem coerentes.
func DeriveSession(priv *ecdh.PrivateKey, peerPubBytes, psk []byte, isClient bool) (*Session, error) {
	peerPub, err := ecdh.X25519().NewPublicKey(peerPubBytes)
	if err != nil {
		return nil, fmt.Errorf("chave pública do par inválida: %w", err)
	}
	shared, err := priv.ECDH(peerPub)
	if err != nil {
		return nil, fmt.Errorf("cálculo ECDH falhou: %w", err)
	}

	// salt = SHA-256(PSK): liga as chaves à senha partilhada (anti-MITM).
	saltSum := sha256.Sum256(psk)
	salt := saltSum[:]

	// transcript = ambas as chaves públicas em ordem canónica (igual nos dois
	// lados): qualquer substituição de chave pelo relay invalida a derivação.
	transcript := orderedConcat(priv.PublicKey().Bytes(), peerPubBytes)

	keyC2O, err := deriveKey(shared, salt, infoC2O, transcript)
	if err != nil {
		return nil, err
	}
	keyO2C, err := deriveKey(shared, salt, infoO2C, transcript)
	if err != nil {
		return nil, err
	}

	var sendKey, recvKey []byte
	if isClient {
		sendKey, recvKey = keyC2O, keyO2C
	} else {
		sendKey, recvKey = keyO2C, keyC2O
	}

	sendAEAD, err := aeadFromKey(sendKey)
	if err != nil {
		return nil, err
	}
	recvAEAD, err := aeadFromKey(recvKey)
	if err != nil {
		return nil, err
	}
	return &Session{send: sendAEAD, recv: recvAEAD}, nil
}

// Seal cifra e autentica o texto em claro, devolvendo o nonce usado e o
// ciphertext. O nonce avança monotonicamente (contador por sentido).
func (s *Session) Seal(plaintext []byte) (nonce, ciphertext []byte) {
	nonce = nonceFromCounter(s.sendCtr)
	s.sendCtr++
	ciphertext = s.send.Seal(nil, nonce, plaintext, nil)
	return nonce, ciphertext
}

// ErrReplay indica um nonce repetido ou fora de ordem (possível ataque).
var ErrReplay = errors.New("nonce repetido ou fora de ordem (possível replay)")

// Open verifica e decifra. Rejeita nonces não-crescentes (anti-replay).
func (s *Session) Open(nonce, ciphertext []byte) ([]byte, error) {
	if len(nonce) != nonceSize {
		return nil, errors.New("tamanho de nonce inválido")
	}
	ctr := binary.BigEndian.Uint64(nonce[4:])
	if s.recvSeen && ctr <= s.recvCtr {
		return nil, ErrReplay
	}
	plaintext, err := s.recv.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decifragem/autenticação falhou: %w", err)
	}
	s.recvCtr = ctr
	s.recvSeen = true
	return plaintext, nil
}

// --- Helpers internos -------------------------------------------------------

func deriveKey(secret, salt []byte, info string, transcript []byte) ([]byte, error) {
	full := append([]byte(info), transcript...)
	r := hkdf.New(sha256.New, secret, salt, full)
	key := make([]byte, keySize)
	if _, err := io.ReadFull(r, key); err != nil {
		return nil, fmt.Errorf("HKDF falhou: %w", err)
	}
	return key, nil
}

func aeadFromKey(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("AES: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("GCM: %w", err)
	}
	return aead, nil
}

func nonceFromCounter(ctr uint64) []byte {
	n := make([]byte, nonceSize)
	binary.BigEndian.PutUint64(n[4:], ctr) // 4 bytes zero + 8 bytes contador
	return n
}

func orderedConcat(a, b []byte) []byte {
	if bytesLess(a, b) {
		return append(append([]byte{}, a...), b...)
	}
	return append(append([]byte{}, b...), a...)
}

func bytesLess(a, b []byte) bool {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}
