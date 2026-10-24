//go:build windows

// Windows 采集：GDI BitBlt 到 DIB section（纯 syscall，无 cgo，交叉编译友好）
package capture

import (
	"image"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")

	pGetSystemMetrics = user32.NewProc("GetSystemMetrics")
	pGetDC            = user32.NewProc("GetDC")
	pReleaseDC        = user32.NewProc("ReleaseDC")
	pCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	pDeleteDC         = gdi32.NewProc("DeleteDC")
	pSelectObject     = gdi32.NewProc("SelectObject")
	pDeleteObject     = gdi32.NewProc("DeleteObject")
	pBitBlt           = gdi32.NewProc("BitBlt")
	pCreateDIBSection = gdi32.NewProc("CreateDIBSection")
)

type gdiScreen struct {
	dc, memDC, dib uintptr
	oldObj        uintptr
	ptr           *byte
	w, h          int
}

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32 // 负值 = top-down
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPels, YPels  int32
	ClrUsed       uint32
	ClrImportant  uint32
}

func sm(i int) int {
	r, _, _ := pGetSystemMetrics.Call(uintptr(i))
	return int(int32(r))
}

// OpenGDI 打开屏幕 DC + DIB section（失败=会话 0 服务/无桌面 → 走 VNC 模式）
func OpenGDI() (Screen, bool) {
	w, h := sm(0), sm(1) // SM_CXSCREEN / SM_CYSCREEN（主屏）
	if w <= 0 || h <= 0 {
		return nil, false
	}
	hdc, _, _ := pGetDC.Call(0)
	if hdc == 0 {
		return nil, false
	}
	memDC, _, _ := pCreateCompatibleDC.Call(hdc)
	bi := bitmapInfoHeader{Size: uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		Width: int32(w), Height: int32(-h), Planes: 1, BitCount: 32, Compression: 0 /*BI_RGB*/}
	var ptr *byte
	dib, _, _ := pCreateDIBSection.Call(hdc, uintptr(unsafe.Pointer(&bi)), 0,
		uintptr(unsafe.Pointer(&ptr)), 0, 0)
	if dib == 0 || ptr == nil {
		pDeleteDC.Call(memDC)
		pReleaseDC.Call(0, hdc)
		return nil, false
	}
	old, _, _ := pSelectObject.Call(memDC, dib)
	return &gdiScreen{dc: hdc, memDC: memDC, dib: dib, oldObj: old, ptr: ptr, w: w, h: h}, true
}

func (s *gdiScreen) Size() image.Point { return image.Pt(s.w, s.h) }

func (s *gdiScreen) Frame(_ []byte) ([]byte, error) {
	// SRCCOPY|CAPTUREBLT：含分层窗口
	_, _, err := pBitBlt.Call(s.memDC, 0, 0, uintptr(s.w), uintptr(s.h), s.dc, 0, 0,
		uintptr(0x00CC0020|0x40000000))
	if err != nil && err.(windows.Errno) != 0 {
		return nil, err
	}
	return unsafe.Slice(s.ptr, s.w*s.h*4), nil
}

func (s *gdiScreen) Close() {
	pSelectObject.Call(s.memDC, s.oldObj)
	pDeleteObject.Call(s.dib)
	pDeleteDC.Call(s.memDC)
	pReleaseDC.Call(0, s.dc)
}

func open() (Screen, bool) { scr, ok := OpenGDI(); return scr, ok }
