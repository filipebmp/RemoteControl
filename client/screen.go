//go:build windows

// screen.go — captura de ecrã no Windows via GDI, com suporte multi-monitor.
//
// Fluxo de captura:
//   GetDC(0) -> CreateCompatibleDC -> CreateCompatibleBitmap -> BitBlt ->
//   GetDIBits (lê os pixels BGRA) -> conversão para RGBA -> jpeg.Encode.
//
// A deteção de monitores usa EnumDisplayMonitors (user32.dll).
package main

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"syscall"
	"unsafe"
)

// DLLs partilhadas por todo o pacote (screen.go, input.go, main.go).
var (
	user32 = syscall.NewLazyDLL("user32.dll")
	gdi32  = syscall.NewLazyDLL("gdi32.dll")

	procEnumDisplayMonitors = user32.NewProc("EnumDisplayMonitors")
	procGetMonitorInfoW     = user32.NewProc("GetMonitorInfoW")
	procGetSystemMetrics    = user32.NewProc("GetSystemMetrics")
	procGetDC               = user32.NewProc("GetDC")
	procReleaseDC           = user32.NewProc("ReleaseDC")

	procCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	procCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	procSelectObject           = gdi32.NewProc("SelectObject")
	procBitBlt                 = gdi32.NewProc("BitBlt")
	procGetDIBits              = gdi32.NewProc("GetDIBits")
	procDeleteObject           = gdi32.NewProc("DeleteObject")
	procDeleteDC               = gdi32.NewProc("DeleteDC")
)

const (
	smXVirtualScreen  = 76
	smYVirtualScreen  = 77
	smCXVirtualScreen = 78
	smCYVirtualScreen = 79

	monitorInfoFlagPrimary = 0x1
	cchDeviceName          = 32

	srccopy      = 0x00CC0020
	captureblt   = 0x40000000
	biRGB        = 0
	dibRGBColors = 0
)

type rect struct{ Left, Top, Right, Bottom int32 }

type monitorInfoEx struct {
	cbSize    uint32
	rcMonitor rect
	rcWork    rect
	dwFlags   uint32
	szDevice  [cchDeviceName]uint16
}

// Monitor descreve um ecrã (ou o desktop virtual completo).
type Monitor struct {
	Index   int
	Device  string
	X, Y    int // origem no desktop virtual
	W, H    int
	Primary bool
}

func getSystemMetrics(i int) int {
	r, _, _ := procGetSystemMetrics.Call(uintptr(i))
	return int(int32(r))
}

// ListMonitors enumera todos os ecrãs físicos ligados.
func ListMonitors() ([]Monitor, error) {
	var mons []Monitor
	cb := syscall.NewCallback(func(h, _hdc, _lprc, _lparam uintptr) uintptr {
		var mi monitorInfoEx
		mi.cbSize = uint32(unsafe.Sizeof(mi))
		if r, _, _ := procGetMonitorInfoW.Call(h, uintptr(unsafe.Pointer(&mi))); r != 0 {
			mons = append(mons, Monitor{
				Index:   len(mons),
				Device:  syscall.UTF16ToString(mi.szDevice[:]),
				X:       int(mi.rcMonitor.Left),
				Y:       int(mi.rcMonitor.Top),
				W:       int(mi.rcMonitor.Right - mi.rcMonitor.Left),
				H:       int(mi.rcMonitor.Bottom - mi.rcMonitor.Top),
				Primary: mi.dwFlags&monitorInfoFlagPrimary != 0,
			})
		}
		return 1 // continuar enumeração
	})
	r, _, err := procEnumDisplayMonitors.Call(0, 0, cb, 0)
	if r == 0 {
		return nil, fmt.Errorf("EnumDisplayMonitors falhou: %w", err)
	}
	if len(mons) == 0 {
		return nil, fmt.Errorf("nenhum monitor detetado")
	}
	return mons, nil
}

// VirtualScreen devolve o retângulo que engloba todos os monitores.
func VirtualScreen() Monitor {
	return Monitor{
		Index:  -1,
		Device: "VIRTUAL (todos os monitores)",
		X:      getSystemMetrics(smXVirtualScreen),
		Y:      getSystemMetrics(smYVirtualScreen),
		W:      getSystemMetrics(smCXVirtualScreen),
		H:      getSystemMetrics(smCYVirtualScreen),
	}
}

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type bitmapInfo struct {
	Header bitmapInfoHeader
	Colors [1]uint32
}

// CaptureRGBA captura a região (x,y,w,h) do desktop virtual para uma imagem.
func CaptureRGBA(x, y, w, h int) (*image.RGBA, error) {
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("dimensões inválidas: %dx%d", w, h)
	}

	screenDC, _, _ := procGetDC.Call(0)
	if screenDC == 0 {
		return nil, fmt.Errorf("GetDC falhou")
	}
	defer procReleaseDC.Call(0, screenDC)

	memDC, _, _ := procCreateCompatibleDC.Call(screenDC)
	if memDC == 0 {
		return nil, fmt.Errorf("CreateCompatibleDC falhou")
	}
	defer procDeleteDC.Call(memDC)

	bmp, _, _ := procCreateCompatibleBitmap.Call(screenDC, uintptr(w), uintptr(h))
	if bmp == 0 {
		return nil, fmt.Errorf("CreateCompatibleBitmap falhou")
	}
	defer procDeleteObject.Call(bmp)

	old, _, _ := procSelectObject.Call(memDC, bmp)
	defer procSelectObject.Call(memDC, old)

	// CAPTUREBLT inclui janelas em camada (layered); importante para capturar
	// corretamente certos elementos de UI.
	if r, _, err := procBitBlt.Call(
		memDC, 0, 0, uintptr(w), uintptr(h),
		screenDC, uintptr(int32(x)), uintptr(int32(y)),
		srccopy|captureblt,
	); r == 0 {
		return nil, fmt.Errorf("BitBlt falhou: %w", err)
	}

	var bi bitmapInfo
	bi.Header.Size = uint32(unsafe.Sizeof(bi.Header))
	bi.Header.Width = int32(w)
	bi.Header.Height = -int32(h) // negativo => top-down (linha 0 no topo)
	bi.Header.Planes = 1
	bi.Header.BitCount = 32
	bi.Header.Compression = biRGB

	buf := make([]byte, w*h*4)
	if r, _, err := procGetDIBits.Call(
		memDC, bmp, 0, uintptr(h),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&bi)),
		dibRGBColors,
	); r == 0 {
		return nil, fmt.Errorf("GetDIBits falhou: %w", err)
	}

	// GDI entrega BGRA; convertemos para RGBA (ordem do image.RGBA).
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(buf); i += 4 {
		img.Pix[i+0] = buf[i+2] // R <- B
		img.Pix[i+1] = buf[i+1] // G
		img.Pix[i+2] = buf[i+0] // B <- R
		img.Pix[i+3] = 0xff     // A (opaco)
	}
	return img, nil
}

// CaptureJPEG captura a região e devolve-a comprimida em JPEG.
func CaptureJPEG(x, y, w, h, quality int) ([]byte, error) {
	img, err := CaptureRGBA(x, y, w, h)
	if err != nil {
		return nil, err
	}
	if quality < 1 {
		quality = 1
	}
	if quality > 100 {
		quality = 100
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, fmt.Errorf("codificar JPEG: %w", err)
	}
	return b.Bytes(), nil
}
