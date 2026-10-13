//go:build !windows

// Stub para plataformas não-Windows: o cliente depende de APIs Win32
// (captura GDI e injeção via user32.dll) e só é funcional em Windows.
// Este ficheiro permite que `go build ./...` e `go vet ./...` corram
// noutros SO (ex.: no servidor de CI Linux) sem erros de build.
package main

import "fmt"

func main() {
	fmt.Println("O cliente RemoteControl só é suportado em Windows. " +
		"Compile com: GOOS=windows GOARCH=amd64 go build ./client")
}
