//go:build windows

// Windows 剪贴板：user32 OpenClipboard/GetClipboardData/SetClipboardData +
// kernel32 GlobalAlloc 可移动内存（SetClipboardData 接管所有权，不得释放）
package clipboard

import (
	"sync"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	pOpenClipboard    = user32.NewProc("OpenClipboard")
	pCloseClipboard   = user32.NewProc("CloseClipboard")
	pEmptyClipboard   = user32.NewProc("EmptyClipboard")
	pGetClipboardData = user32.NewProc("GetClipboardData")
	pSetClipboardData = user32.NewProc("SetClipboardData")

	pGlobalAlloc  = kernel32.NewProc("GlobalAlloc")
	pGlobalLock   = kernel32.NewProc("GlobalLock")
	pGlobalUnlock = kernel32.NewProc("GlobalUnlock")
	pGlobalFree   = kernel32.NewProc("GlobalFree")
)

const cfUnicodeText = 13

type winBoard struct{ mu sync.Mutex }

func open() (Board, bool) {
	b := &winBoard{}
	// 探测 OpenClipboard 可调即视为可用（剪贴板恰为空时 Get 也返回 false）
	if r, _, _ := pOpenClipboard.Call(0); r != 0 {
		_, _, _ = pCloseClipboard.Call()
		return b, true
	}
	return nil, false
}

// OpenClipboard 带重试：别的进程可能正持有剪贴板
func openClipboard() bool {
	for i := 0; i < 5; i++ {
		if r, _, _ := pOpenClipboard.Call(0); r != 0 {
			return true
		}
		// 50ms 后重试
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func (b *winBoard) Get() (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !openClipboard() {
		return "", false
	}
	defer pCloseClipboard.Call()
	h, _, _ := pGetClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return "", false
	}
	ptr, _, _ := pGlobalLock.Call(h)
	if ptr == 0 {
		return "", false
	}
	defer pGlobalUnlock.Call(h)
	// UTF-16 序列读到双 NUL 终止，封顶 256K 字符
	u16 := make([]uint16, 0, 4096)
	p := unsafe.Pointer(ptr)
	for i := 0; i < 256*1024; i++ {
		c := *(*uint16)(unsafe.Add(p, i*2))
		if c == 0 {
			break
		}
		u16 = append(u16, c)
	}
	return string(utf16.Decode(u16)), true
}

func (b *winBoard) Set(text string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	u16 := utf16.Encode([]rune(text))
	size := uintptr((len(u16) + 1) * 2)
	h, _, _ := pGlobalAlloc.Call(0x0042, size) // GMEM_MOVEABLE|GMEM_ZEROINIT
	if h == 0 {
		return false
	}
	ptr, _, _ := pGlobalLock.Call(h)
	if ptr == 0 {
		pGlobalFree.Call(h)
		return false
	}
	dst := unsafe.Slice((*uint16)(unsafe.Pointer(ptr)), len(u16)+1)
	copy(dst, u16)
	dst[len(u16)] = 0
	pGlobalUnlock.Call(h)

	if !openClipboard() {
		pGlobalFree.Call(h)
		return false
	}
	defer pCloseClipboard.Call()
	pEmptyClipboard.Call()
	if r, _, _ := pSetClipboardData.Call(cfUnicodeText, h); r == 0 {
		pGlobalFree.Call(h) // 失败时回收，防泄漏
		return false
	}
	return true // 成功：h 所有权移交剪贴板，禁止再释放
}

func (b *winBoard) Close() {}
