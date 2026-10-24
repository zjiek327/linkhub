// Package input 平台输入注入（纯 syscall/协议，无 cgo）
package input

// Injector 平台输入注入器
type Injector interface {
	// MouseMove 绝对坐标移动（屏幕像素）
	MouseMove(x, y int) error
	// MouseButton 按下/释放（button: 1左 2中 3右 4滚上 5滚下）
	MouseButton(button int, down bool) error
	// Wheel 滚轮（dy>0 向下）
	Wheel(dy int) error
	// Key 按键（keysym，见 keysyms.go）
	Key(keysym uint32, down bool) error
	// Close 释放
	Close()
}

// Open 打开平台注入器（失败返回 ok=false → 该会话只读）
func Open() (Injector, bool) { return open() }
