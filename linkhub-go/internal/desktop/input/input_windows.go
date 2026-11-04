//go:build windows

// Windows 输入注入：SendInput（绝对坐标用虚拟桌面归一化）
package input

import (
	"log"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var lastInputErrLog time.Time

// logInputErr 注入失败的限频日志：SetCursorPos/SendInput 失败是静默返回 0，
// 不主动检查就永远丢失——鼠标失控定位的最后一段（Windows 错误码直出）
func logInputErr(what string, err error) {
	if time.Since(lastInputErrLog) < 2*time.Second {
		return
	}
	lastInputErrLog = time.Now()
	log.Printf("[input] %s 失败: %v", what, err)
}

var (
	user32            = windows.NewLazySystemDLL("user32.dll")
	pSendInput        = user32.NewProc("SendInput")
	pSetCursorPos     = user32.NewProc("SetCursorPos")
	pGetSystemMetrics = user32.NewProc("GetSystemMetrics")
	pGetCursorInfo    = user32.NewProc("GetCursorInfo")
	pDrawIconEx       = user32.NewProc("DrawIconEx")
)

const (
	inputMouse = 0
	inputKeyboard = 1

	mouseEventfMove      = 0x0001
	mouseEventfLeftDown  = 0x0002
	mouseEventfLeftUp    = 0x0004
	mouseEventfRightDown = 0x0008
	mouseEventfRightUp   = 0x0010
	mouseEventfMiddleDown = 0x0020
	mouseEventfMiddleUp  = 0x0040
	mouseEventfWheel     = 0x0800
	mouseEventfAbsolute  = 0x8000

	keyEventfKeyUp = 0x0002
)

// SendInput 的 cbSize 必须传 sizeof(INPUT)。INPUT 是联合体，按最大的成员
// MOUSEINPUT 对齐（x64=40/386=28），与本次注入的事件类型无关——键盘事件传
// sizeof(KEYBDINPUT 布局 32) 会被 Windows 判非法直接返回 0（键盘静默失效的
// 根因），鼠标事件恰因 mouseInput 就是最大成员而侥幸正确。
var sizeofINPUT = uint32(unsafe.Sizeof(mouseInput{}))

type mouseInput struct {
	Type uint32
	Mi   struct {
		Dx, Dy      int32
		MouseData   uint32
		Flags       uint32
		Time        uint32
		ExtraInfo   uintptr
	}
}

type keyboardInput struct {
	Type uint32
	Ki   struct {
		WVk         uint16
		WScan       uint16
		Flags       uint32
		Time        uint32
		ExtraInfo   uintptr
	}
}

type winInput struct{}

func open() (Injector, bool) { return winInput{}, true }

func sm(i int) int {
	r, _, _ := pGetSystemMetrics.Call(uintptr(i))
	return int(int32(r))
}

func (winInput) sendMouse(flags uint32, dx, dy int32, data uint32) error {
	var mi mouseInput
	mi.Type = inputMouse
	mi.Mi.Dx, mi.Mi.Dy, mi.Mi.MouseData, mi.Mi.Flags = dx, dy, data, flags
	n, _, err := pSendInput.Call(1, uintptr(unsafe.Pointer(&mi)), uintptr(sizeofINPUT))
	if n != 1 {
		logInputErr("SendInput mouse", err)
	}
	return nil
}

func (winInput) sendKey(vk uint16, down bool) error {
	var ki keyboardInput
	ki.Type = inputKeyboard
	ki.Ki.WVk = vk
	if !down {
		ki.Ki.Flags = keyEventfKeyUp
	}
	n, _, err := pSendInput.Call(1, uintptr(unsafe.Pointer(&ki)), uintptr(sizeofINPUT))
	if n != 1 {
		logInputErr("SendInput key", err)
	}
	return nil
}

func (w winInput) MouseMove(x, y int) error {
	// SetCursorPos：直接像素坐标，无 SendInput 绝对坐标的归一化歧义
	if r, _, err := pSetCursorPos.Call(uintptr(x), uintptr(y)); r == 0 {
		logInputErr("SetCursorPos", err)
	}
	return nil
}

func (w winInput) MouseButton(button int, down bool) error {
	var flags uint32
	switch button {
	case 1:
		flags = mouseEventfLeftDown
		if !down {
			flags = mouseEventfLeftUp
		}
	case 2:
		flags = mouseEventfMiddleDown
		if !down {
			flags = mouseEventfMiddleUp
		}
	case 3:
		flags = mouseEventfRightDown
		if !down {
			flags = mouseEventfRightUp
		}
	default:
		return nil
	}
	return w.sendMouse(flags, 0, 0, 0)
}

func (w winInput) Wheel(dy int) error {
	d := uint32(uint32(int32(-dy*120)) & 0xffffffff) // Windows 滚轮正值向上
	return w.sendMouse(mouseEventfWheel, 0, 0, d)
}

// Key keysym（X11 符号）→ Windows VK：经 keysyms.go 的映射表转换
func (w winInput) Key(keysym uint32, down bool) error {
	vk, ok := keysymToVK(keysym)
	if !ok {
		return nil
	}
	return w.sendKey(vk, down)
}

func (winInput) ReleaseAll() error {
	// 无条件补放全部修饰键：keyup 重复是无害的
	for _, vk := range []uint16{0x10 /*SHIFT*/, 0x11 /*CONTROL*/, 0x12 /*MENU*/, 0x5B /*LWIN*/, 0x5C /*RWIN*/} {
		var ki keyboardInput
		ki.Type = inputKeyboard
		ki.Ki.WVk = vk
		ki.Ki.Flags = keyEventfKeyUp
		pSendInput.Call(1, uintptr(unsafe.Pointer(&ki)), uintptr(sizeofINPUT))
	}
	return nil
}

func (winInput) Close() {}
