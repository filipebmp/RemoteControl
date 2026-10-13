//go:build windows

// input.go — injeção de rato e teclado no Windows via user32.dll.
//
// Usa SendInput (recomendado pela Microsoft em vez de mouse_event/keybd_event)
// e SetCursorPos para o posicionamento absoluto do cursor.
package main

import (
	"fmt"
	"unsafe"
)

var (
	procSendInput    = user32.NewProc("SendInput")
	procSetCursorPos = user32.NewProc("SetCursorPos")
)

const (
	inputMouse    = 0
	inputKeyboard = 1

	mouseeventfLeftDown   = 0x0002
	mouseeventfLeftUp     = 0x0004
	mouseeventfRightDown  = 0x0008
	mouseeventfRightUp    = 0x0010
	mouseeventfMiddleDown = 0x0020
	mouseeventfMiddleUp   = 0x0040
	mouseeventfWheel      = 0x0800

	keyeventfKeyUp   = 0x0002
	keyeventfUnicode = 0x0004

	wheelDelta = 120
)

// inputStruct corresponde à estrutura INPUT do Win32 em x64 (40 bytes).
// A união MOUSEINPUT/KEYBDINPUT é representada por um buffer de 32 bytes,
// preenchido manualmente conforme o tipo — evitando problemas de alinhamento
// e aliasing de uniões em Go.
type inputStruct struct {
	inputType uint32
	_         uint32   // padding: alinha a união a 8 bytes
	data      [32]byte // MOUSEINPUT ou KEYBDINPUT
}

func sendInputs(ins []inputStruct) error {
	if len(ins) == 0 {
		return nil
	}
	n, _, err := procSendInput.Call(
		uintptr(len(ins)),
		uintptr(unsafe.Pointer(&ins[0])),
		unsafe.Sizeof(ins[0]),
	)
	if int(n) != len(ins) {
		return fmt.Errorf("SendInput injetou %d de %d eventos: %w", n, len(ins), err)
	}
	return nil
}

// le escreve um uint32 little-endian no offset indicado do buffer da união.
func (in *inputStruct) le(off int, v uint32) {
	in.data[off+0] = byte(v)
	in.data[off+1] = byte(v >> 8)
	in.data[off+2] = byte(v >> 16)
	in.data[off+3] = byte(v >> 24)
}

// putMouse constrói um evento de rato.
// Layout MOUSEINPUT: dx(4) dy(4) mouseData(4) dwFlags(4) time(4) extra(8).
func putMouse(dx, dy int32, mouseData, flags uint32) inputStruct {
	var in inputStruct
	in.inputType = inputMouse
	in.le(0, uint32(dx))
	in.le(4, uint32(dy))
	in.le(8, mouseData)
	in.le(12, flags)
	return in
}

// putKey constrói um evento de teclado.
// Layout KEYBDINPUT: wVk(2) wScan(2) dwFlags(4) time(4) extra(8).
func putKey(vk, scan uint16, flags uint32) inputStruct {
	var in inputStruct
	in.inputType = inputKeyboard
	in.data[0] = byte(vk)
	in.data[1] = byte(vk >> 8)
	in.data[2] = byte(scan)
	in.data[3] = byte(scan >> 8)
	in.le(4, flags)
	return in
}

// MoveMouse posiciona o cursor em coordenadas absolutas (pixels do desktop).
func MoveMouse(x, y int) error {
	if r, _, err := procSetCursorPos.Call(uintptr(int32(x)), uintptr(int32(y))); r == 0 {
		return fmt.Errorf("SetCursorPos falhou: %w", err)
	}
	return nil
}

// ClickButton simula um clique completo (down + up) do botão indicado.
func ClickButton(button string) error {
	var down, up uint32
	switch button {
	case "right":
		down, up = mouseeventfRightDown, mouseeventfRightUp
	case "middle":
		down, up = mouseeventfMiddleDown, mouseeventfMiddleUp
	default: // "left" ou vazio
		down, up = mouseeventfLeftDown, mouseeventfLeftUp
	}
	return sendInputs([]inputStruct{
		putMouse(0, 0, 0, down),
		putMouse(0, 0, 0, up),
	})
}

// ScrollWheel roda a roda do rato (delta em "cliques"; positivo = para cima).
func ScrollWheel(delta int) error {
	return sendInputs([]inputStruct{
		putMouse(0, 0, uint32(int32(delta*wheelDelta)), mouseeventfWheel),
	})
}

// KeyStroke pressiona e solta uma virtual-key (VK_*).
func KeyStroke(vk uint16) error {
	return sendInputs([]inputStruct{
		putKey(vk, 0, 0),
		putKey(vk, 0, keyeventfKeyUp),
	})
}

// TypeText digita texto arbitrário via eventos Unicode (independente do layout
// de teclado). Limita-se ao plano BMP (caracteres de 1 unidade UTF-16).
func TypeText(s string) error {
	var ins []inputStruct
	for _, r := range s {
		if r > 0xFFFF {
			continue // ignora caracteres fora do BMP (ex.: emojis)
		}
		u := uint16(r)
		ins = append(ins,
			putKey(0, u, keyeventfUnicode),
			putKey(0, u, keyeventfUnicode|keyeventfKeyUp),
		)
	}
	return sendInputs(ins)
}
