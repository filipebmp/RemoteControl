//go:build windows

// Package screen — extensão de deteção multi-monitor para Windows.
//
// Usa a API Win32 EnumDisplayMonitors (user32.dll) para enumerar todos os
// ecrãs físicos ligados e expor os seus limites (bounds), nome de dispositivo
// e qual deles é o monitor principal. Permite ao operador escolher:
//
//   - transmitir um monitor específico (Monitor.Bounds), ou
//   - transmitir toda a área de trabalho estendida (VirtualScreenBounds),
//     que engloba todos os monitores num único retângulo virtual.
//
// Integração com o resto de screen.go: o capturador de ecrã deve receber um
// retângulo (Rect) e capturar apenas essa região. Use SelectCaptureRegion
// abaixo para resolver a escolha do utilizador num Rect pronto a capturar.
package screen

import (
	"fmt"
	"syscall"
	"unsafe"
)

// ---------------------------------------------------------------------------
// Ligações às DLLs do Windows
// ---------------------------------------------------------------------------
var (
	user32                  = syscall.NewLazyDLL("user32.dll")
	procEnumDisplayMonitors = user32.NewProc("EnumDisplayMonitors")
	procGetMonitorInfoW     = user32.NewProc("GetMonitorInfoW")
	procGetSystemMetrics    = user32.NewProc("GetSystemMetrics")
)

// Índices de GetSystemMetrics para a área de trabalho virtual (desktop estendido).
const (
	smXVirtualScreen  = 76
	smYVirtualScreen  = 77
	smCXVirtualScreen = 78
	smCYVirtualScreen = 79

	monitorInfoFlagPrimary = 0x00000001 // MONITORINFOF_PRIMARY
	cchDeviceName          = 32         // CCHDEVICENAME
)

// rect espelha a estrutura RECT do Win32.
type rect struct {
	Left, Top, Right, Bottom int32
}

// monitorInfoEx espelha MONITORINFOEXW.
type monitorInfoEx struct {
	cbSize    uint32
	rcMonitor rect
	rcWork    rect
	dwFlags   uint32
	szDevice  [cchDeviceName]uint16
}

// ---------------------------------------------------------------------------
// Tipos públicos
// ---------------------------------------------------------------------------

// Rect representa uma região retangular do ecrã em coordenadas virtuais.
type Rect struct {
	X, Y, W, H int
}

func (r Rect) String() string {
	return fmt.Sprintf("%dx%d @ (%d,%d)", r.W, r.H, r.X, r.Y)
}

// Monitor descreve um ecrã físico ligado ao sistema.
type Monitor struct {
	Index   int    // índice estável (0..n-1) pela ordem de enumeração
	Device  string // nome do dispositivo, ex: "\\.\DISPLAY1"
	Bounds  Rect   // limites deste monitor em coordenadas virtuais
	Primary bool   // true se for o monitor principal
	handle  uintptr
}

func (m Monitor) String() string {
	tag := ""
	if m.Primary {
		tag = " [principal]"
	}
	return fmt.Sprintf("#%d %s %s%s", m.Index, m.Device, m.Bounds, tag)
}

// ---------------------------------------------------------------------------
// API
// ---------------------------------------------------------------------------

// ListMonitors enumera todos os monitores ligados através de EnumDisplayMonitors.
// A ordem é estável dentro da mesma configuração de ecrãs.
func ListMonitors() ([]Monitor, error) {
	var monitors []Monitor

	// Callback invocado pelo Windows uma vez por cada monitor.
	// Assinatura Win32: BOOL CALLBACK (HMONITOR, HDC, LPRECT, LPARAM)
	callback := syscall.NewCallback(func(hMonitor, _hdc, _lprc, _lparam uintptr) uintptr {
		var mi monitorInfoEx
		mi.cbSize = uint32(unsafe.Sizeof(mi))

		ret, _, _ := procGetMonitorInfoW.Call(
			hMonitor,
			uintptr(unsafe.Pointer(&mi)),
		)
		if ret == 0 {
			// Ignora este monitor mas continua a enumeração.
			return 1
		}

		monitors = append(monitors, Monitor{
			Index:   len(monitors),
			Device:  syscall.UTF16ToString(mi.szDevice[:]),
			Bounds:  rectToRect(mi.rcMonitor),
			Primary: mi.dwFlags&monitorInfoFlagPrimary != 0,
			handle:  hMonitor,
		})
		return 1 // 1 = continuar a enumerar
	})

	ret, _, err := procEnumDisplayMonitors.Call(
		0, // HDC nulo -> enumerar todos os ecrãs
		0, // sem retângulo de clip
		callback,
		0, // dwData não usado
	)
	if ret == 0 {
		return nil, fmt.Errorf("EnumDisplayMonitors falhou: %w", err)
	}
	if len(monitors) == 0 {
		return nil, fmt.Errorf("nenhum monitor detetado")
	}
	return monitors, nil
}

// HasMultipleMonitors devolve true se existir mais do que um ecrã ligado.
func HasMultipleMonitors() (bool, error) {
	mons, err := ListMonitors()
	if err != nil {
		return false, err
	}
	return len(mons) > 1, nil
}

// PrimaryMonitor devolve o monitor principal.
func PrimaryMonitor() (Monitor, error) {
	mons, err := ListMonitors()
	if err != nil {
		return Monitor{}, err
	}
	for _, m := range mons {
		if m.Primary {
			return m, nil
		}
	}
	return mons[0], nil // fallback: o primeiro
}

// VirtualScreenBounds devolve o retângulo que engloba TODOS os monitores
// (a área de trabalho estendida completa). Útil para transmitir todos os
// ecrãs de uma só vez. Pode ter origem negativa se existirem monitores à
// esquerda/acima do principal.
func VirtualScreenBounds() Rect {
	x := getSystemMetrics(smXVirtualScreen)
	y := getSystemMetrics(smYVirtualScreen)
	w := getSystemMetrics(smCXVirtualScreen)
	h := getSystemMetrics(smCYVirtualScreen)
	return Rect{X: x, Y: y, W: w, H: h}
}

// Target identifica o que o utilizador escolheu transmitir.
type Target struct {
	// FullDesktop = true -> transmitir toda a área estendida (todos os ecrãs).
	FullDesktop bool
	// MonitorIndex -> quando FullDesktop é false, o índice do monitor a usar.
	MonitorIndex int
}

// SelectCaptureRegion traduz a escolha do utilizador num Rect pronto a ser
// entregue ao capturador de ecrã.
//
//	// Exemplo: transmitir o monitor #1
//	region, err := screen.SelectCaptureRegion(screen.Target{MonitorIndex: 1})
//
//	// Exemplo: transmitir o desktop estendido inteiro
//	region, err := screen.SelectCaptureRegion(screen.Target{FullDesktop: true})
func SelectCaptureRegion(t Target) (Rect, error) {
	if t.FullDesktop {
		return VirtualScreenBounds(), nil
	}
	mons, err := ListMonitors()
	if err != nil {
		return Rect{}, err
	}
	if t.MonitorIndex < 0 || t.MonitorIndex >= len(mons) {
		return Rect{}, fmt.Errorf(
			"monitor #%d inválido: existem %d monitores (0..%d)",
			t.MonitorIndex, len(mons), len(mons)-1)
	}
	return mons[t.MonitorIndex].Bounds, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func getSystemMetrics(index int) int {
	ret, _, _ := procGetSystemMetrics.Call(uintptr(index))
	return int(int32(ret))
}

func rectToRect(r rect) Rect {
	return Rect{
		X: int(r.Left),
		Y: int(r.Top),
		W: int(r.Right - r.Left),
		H: int(r.Bottom - r.Top),
	}
}
