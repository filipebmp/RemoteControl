module github.com/filipebmp/remotecontrol

go 1.22

require (
	github.com/gorilla/websocket v1.5.3
	golang.org/x/crypto v0.28.0
)

// NOTA: execute `go mod tidy` para descarregar as dependências e gerar o
// go.sum (não incluído porque requer acesso à rede ao proxy de módulos).
