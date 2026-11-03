// Package clipboard 平台系统剪贴板读写（纯 Go 系统调用/xgb，无 cgo）
package clipboard

// Board 系统剪贴板（纯文本）
type Board interface {
	// Get 读当前剪贴板文本；无内容/不可用返回 ok=false
	Get() (text string, ok bool)
	// Set 写入文本并设为剪贴板内容；成功返回 true
	Set(text string) bool
	// Close 释放底层资源（进程长存场景幂等）
	Close()
}

// Open 打开系统剪贴板；不支持/失败返回 nil,false
func Open() (Board, bool) { return open() }
