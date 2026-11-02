//go:build windows

// Windows 采集：GDI BitBlt 到 DIB section（纯 syscall，无 cgo，交叉编译友好）
package capture

import (
	"image"
	"sync"
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
	pGetCursorInfo    = user32.NewProc("GetCursorInfo")
	pDrawIconEx       = user32.NewProc("DrawIconEx")
	pSetDpiAware      = user32.NewProc("SetProcessDpiAwarenessContext")
)

// Per-Monitor V2 DPI 感知：此后 GetSystemMetrics/GetDC 一律返回物理像素。
// 不设的话高 DPI（125%/150% 缩放）下拿到的是虚拟化缩小分辨率，
// Mag 采集源按物理坐标只抓左上角一块，GDI 抓帧也整体降分辨率。
func init() { pSetDpiAware.Call(uintptr(0xfffffffffffffffc) /*PER_MONITOR_AWARE_V2*/) }

type gdiScreen struct {
	dc, memDC, dib uintptr
	oldObj        uintptr
	ptr           *byte
	w, h          int
	mu            sync.Mutex // Frame 串行化：DIB 为进程级单例共享
}

// cursorInfo CURSORINFO（GetCursorInfo）
type cursorInfo struct {
	cbSize  uint32
	flags   uint32
	hCursor uintptr
	ptX     int32
	ptY     int32
}

const cursorShowing = 0x00000001

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

func (s *gdiScreen) Frame(buf []byte) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// SRCCOPY|CAPTUREBLT：含分层窗口
	_, _, err := pBitBlt.Call(s.memDC, 0, 0, uintptr(s.w), uintptr(s.h), s.dc, 0, 0,
		uintptr(0x00CC0020|0x40000000))
	if err != nil && err.(windows.Errno) != 0 {
		return nil, err
	}
	// BitBlt 不含鼠标光标——补画到帧缓冲，否则远程画面永远看不到指针位置
	var ci cursorInfo
	ci.cbSize = uint32(unsafe.Sizeof(ci))
	if r, _, _ := pGetCursorInfo.Call(uintptr(unsafe.Pointer(&ci))); r != 0 && ci.flags&cursorShowing != 0 {
		// DrawIconEx(hdc, x, y, hIcon, w, h, istep, flickerFree, DI_NORMAL)
		pDrawIconEx.Call(s.memDC, uintptr(ci.ptX), uintptr(ci.ptY), ci.hCursor, 0, 0, 0, 0, 3)
	}
	// DIB 为单例共享缓冲，拷入调用方 buffer（每个 Stream 独立），多会话并发安全
	n := copy(buf, unsafe.Slice(s.ptr, s.w*s.h*4))
	return buf[:n], nil
}

// Close 空操作：DC/DIB 为进程级单例（x11 同款语义），会话结束不能真释放，
// 否则后续会话的 BitBlt 打到已释放句柄上；进程退出时由 OS 回收 GDI 对象
func (s *gdiScreen) Close() {}

func open() (Screen, bool) {
	// 优先 Magnification：能抓 GPU 合成内容（Chrome 硬件加速窗口等）；
	// 不可用（老系统/远程会话）回退 GDI BitBlt
	if scr, ok := OpenMag(); ok {
		return scr, true
	}
	scr, ok := OpenGDI()
	return scr, ok
}
