//go:build windows

// Windows 输入注入：SendInput（绝对坐标用虚拟桌面归一化）
package input

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32          = windows.NewLazySystemDLL("user32.dll")
	pSendInput      = user32.NewProc("SendInput")
	pGetSystemMetrics = user32.NewProc("GetSystemMetrics")
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
	pSendInput.Call(1, uintptr(unsafe.Pointer(&mi)), unsafe.Sizeof(mi))
	return nil
}

func (winInput) sendKey(vk uint16, down bool) error {
	var ki keyboardInput
	ki.Type = inputKeyboard
	ki.Ki.WVk = vk
	if !down {
		ki.Ki.Flags = keyEventfKeyUp
	}
	pSendInput.Call(1, uintptr(unsafe.Pointer(&ki)), unsafe.Sizeof(ki))
	return nil
}

func (w winInput) MouseMove(x, y int) error {
	// 绝对坐标归一化到 0..65535 虚拟桌面
	vw, vh := sm(78), sm(79) // SM_CXVIRTUALSCREEN 用 76/77 宽高即可，这里用主屏
	if vw <= 0 || vh <= 0 {
		vw, vh = sm(0), sm(1)
	}
	dx := int32((x * 65535) / max(vw, 1))
	dy := int32((y * 65535) / max(vh, 1))
	return w.sendMouse(mouseEventfMove|mouseEventfAbsolute, dx, dy, 0)
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

func (winInput) Close() {}
