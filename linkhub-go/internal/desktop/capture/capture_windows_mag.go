//go:build windows

// Magnification API 采集：能抓到 GPU 合成内容（Chrome/Edge 硬件加速窗口、D3D 应用），
// 弥补 GDI BitBlt 的盲区。Mag 渲染依赖窗口消息循环——host/mag 窗口创建与消息泵
// 固定在一个专用 goroutine（Win32 窗口消息只投递到创建线程的队列），
// Frame 通过 channel 请求渲染；任何一步失败返回 false，由 open() 回退 GDI。
package capture

import (
	"errors"
	"fmt"
	"image"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	mag32 = windows.NewLazySystemDLL("Magnification.dll")
	pMagInitialize          = mag32.NewProc("MagInitialize")
	pMagUninitialize        = mag32.NewProc("MagUninitialize")
	pMagCreateWindow        = mag32.NewProc("MagCreateWindow")
	pMagSetWindowSource     = mag32.NewProc("MagSetWindowSource")
	pMagSetWindowFilterList = mag32.NewProc("MagSetWindowFilterList")

	pCreateWindowExW = user32.NewProc("CreateWindowExW")
	pPeekMessageW    = user32.NewProc("PeekMessageW")
	pTranslateMsg    = user32.NewProc("TranslateMessage")
	pDispatchMsg     = user32.NewProc("DispatchMessageW")
	pInvalidateRect  = user32.NewProc("InvalidateRect")
	pSetLayeredAttr  = user32.NewProc("SetLayeredWindowAttributes")
)

const (
	wsExToolWindow  = 0x00000080
	wsExNoActivate  = 0x08000000
	wsExLayered     = 0x00080000
	wsExTransparent = 0x00000020
	wsVisible       = 0x10000000
	pmRemove        = 0x0001
	lwaAlpha        = 0x00000002
)

type rect struct {
	Left, Top, Right, Bottom int32
}

type msgT struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	ptX     int32
	ptY     int32
}

var (
	errMagClosed = errors.New("magnification capture closed")
	errMagTimeout = errors.New("magnification render timeout")
)

// magScreen Magnification 采集实现（Screen 接口）
type magScreen struct {
	w, h          int
	host, magHwnd uintptr
	memDC, dib    uintptr
	ptr           *byte
	mu            sync.Mutex
	reqCh         chan chan error
	done          chan struct{}
	closed        sync.Once
}

// OpenMag 初始化 Magnification 采集；失败返回 false（调用方回退 GDI）
func OpenMag() (Screen, bool) {
	if r, _, _ := pMagInitialize.Call(); r == 0 {
		return nil, false
	}
	w, h := sm(0), sm(1)
	if w <= 0 || h <= 0 {
		return nil, false
	}
	m := &magScreen{w: w, h: h, reqCh: make(chan chan error), done: make(chan struct{})}
	initErr := make(chan error, 1)
	go m.windowThread(w, h, initErr)
	if err := <-initErr; err != nil {
		pMagUninitialize.Call()
		return nil, false
	}
	return m, true
}

// windowThread host/mag 窗口创建 + 渲染服务循环（必须在同一 goroutine：
// Win32 窗口消息只投递到创建线程的队列，跨线程 PeekMessage 拿不到）
func (m *magScreen) windowThread(w, h int, initErr chan error) {
	fail := func(what string) {
		initErr <- magErr(what)
	}

	// host：全透明 layered + 透明命中 + 工具窗——DWM 需要"可见"才渲染 mag 子窗，
	// alpha=1 视觉上不可见且不挡鼠标/焦点
	host, _, _ := pCreateWindowExW.Call(
		wsExToolWindow|wsExNoActivate|wsExLayered|wsExTransparent,
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("Magnifier"))),
		0, wsVisible, 0, 0, uintptr(w), uintptr(h), 0, 0, 0, 0)
	if host == 0 {
		fail("CreateWindowEx(host)")
		return
	}
	pSetLayeredAttr.Call(host, 0, 1, lwaAlpha)

	magHwnd, _, _ := pMagCreateWindow.Call(
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("Magnifier"))),
		host, 0, 0, uintptr(w), uintptr(h), 0, 0)
	if magHwnd == 0 {
		fail("MagCreateWindow")
		return
	}
	src := rect{Left: 0, Top: 0, Right: int32(w), Bottom: int32(h)}
	if r, _, _ := pMagSetWindowSource.Call(magHwnd, uintptr(unsafe.Pointer(&src))); r == 0 {
		fail("MagSetWindowSource")
		return
	}
	// 排除 host 自身，防递归
	pMagSetWindowFilterList.Call(magHwnd, 0 /*MW_FILTERMODE_EXCLUDE*/, 1, uintptr(unsafe.Pointer(&host)))

	// 渲染结果中转 DIB（与 GDI 同款 top-down BGRA）
	hdc, _, _ := pGetDC.Call(0)
	if hdc == 0 {
		fail("GetDC")
		return
	}
	defer pReleaseDC.Call(0, hdc)
	memDC, _, _ := pCreateCompatibleDC.Call(hdc)
	if memDC == 0 {
		fail("CreateCompatibleDC")
		return
	}
	bi := bitmapInfoHeader{Size: uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		Width: int32(w), Height: -int32(h), Planes: 1, BitCount: 32, Compression: 0}
	var ptr *byte
	dib, _, _ := pCreateDIBSection.Call(hdc, uintptr(unsafe.Pointer(&bi)), 0,
		uintptr(unsafe.Pointer(&ptr)), 0, 0)
	if dib == 0 || ptr == nil {
		fail("CreateDIBSection")
		return
	}
	pSelectObject.Call(memDC, dib)
	m.host, m.magHwnd, m.memDC, m.dib, m.ptr = host, magHwnd, memDC, dib, ptr

	initErr <- nil
	close(initErr)

	// 渲染服务循环：Frame 发来请求 → 渲染 → 回执
	for {
		select {
		case <-m.done:
			return
		case resp, ok := <-m.reqCh:
			if !ok {
				return
			}
			resp <- m.renderFrame(w, h)
		}
	}
}

// renderFrame 在窗口线程执行：触发 mag 重绘、泵消息（Mag 库的渲染
// 发生在 DispatchMessage 里 host 的 paint 处理中）、BitBlt mag 子窗到中转 DIB
func (m *magScreen) renderFrame(w, h int) error {
	pInvalidateRect.Call(m.magHwnd, 0, 1)
	for {
		var gm msgT
		r, _, _ := pPeekMessageW.Call(uintptr(unsafe.Pointer(&gm)), 0, 0, 0, pmRemove)
		if r == 0 {
			break
		}
		pTranslateMsg.Call(uintptr(unsafe.Pointer(&gm)))
		pDispatchMsg.Call(uintptr(unsafe.Pointer(&gm)))
	}
	magDC, _, _ := pGetDC.Call(m.magHwnd)
	if magDC == 0 {
		return magErr("GetDC(mag)")
	}
	_, _, e := pBitBlt.Call(m.memDC, 0, 0, uintptr(w), uintptr(h), magDC, 0, 0, 0x00CC0020 /*SRCCOPY*/)
	pReleaseDC.Call(m.magHwnd, magDC)
	if e != nil && e.(windows.Errno) != 0 {
		return e
	}
	return nil
}

func (m *magScreen) Size() image.Point { return image.Pt(m.w, m.h) }

func (m *magScreen) Frame(buf []byte) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	resp := make(chan error, 1)
	select {
	case m.reqCh <- resp:
	case <-m.done:
		return nil, errMagClosed
	}
	select {
	case err := <-resp:
		if err != nil {
			return nil, err
		}
	case <-time.After(3 * time.Second):
		return nil, errMagTimeout
	}
	// 补画光标（BitBlt/Mag 均不含指针；与 GDI 同款）
	var ci cursorInfo
	ci.cbSize = uint32(unsafe.Sizeof(ci))
	if r, _, _ := pGetCursorInfo.Call(uintptr(unsafe.Pointer(&ci))); r != 0 && ci.flags&cursorShowing != 0 {
		pDrawIconEx.Call(m.memDC, uintptr(ci.ptX), uintptr(ci.ptY), ci.hCursor, 0, 0, 0, 0, 3)
	}
	n := copy(buf, unsafe.Slice(m.ptr, m.w*m.h*4))
	return buf[:n], nil
}

// Close 空操作：mag 窗口与 DIB 为进程级单例，随进程存亡（OS 回收）
func (m *magScreen) Close() {
	m.closed.Do(func() { close(m.done) })
}

func magErr(what string) error {
	if e := windows.GetLastError(); e != nil {
		return fmt.Errorf("%s: %w", what, e)
	}
	return fmt.Errorf("%s failed", what)
}
