//go:build !windows

// Stub para plataformas não-Windows. A deteção multi-monitor usa APIs Win32
// e só está disponível no cliente Windows; aqui garantimos apenas que o
// pacote compila em Linux/macOS (p.ex. durante `go vet ./...` no servidor).
package screen

import (
	"fmt"
	"runtime"
)

type Rect struct {
	X, Y, W, H int
}

func (r Rect) String() string { return fmt.Sprintf("%dx%d @ (%d,%d)", r.W, r.H, r.X, r.Y) }

type Monitor struct {
	Index   int
	Device  string
	Bounds  Rect
	Primary bool
}

type Target struct {
	FullDesktop  bool
	MonitorIndex int
}

var errUnsupported = fmt.Errorf("deteção multi-monitor não suportada em %s", runtime.GOOS)

func ListMonitors() ([]Monitor, error)         { return nil, errUnsupported }
func HasMultipleMonitors() (bool, error)       { return false, errUnsupported }
func PrimaryMonitor() (Monitor, error)         { return Monitor{}, errUnsupported }
func VirtualScreenBounds() Rect                { return Rect{} }
func SelectCaptureRegion(Target) (Rect, error) { return Rect{}, errUnsupported }
