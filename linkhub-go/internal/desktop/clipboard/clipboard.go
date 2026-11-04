// Package clipboard 平台系统剪贴板读写（纯 Go 系统调用/xgb，无 cgo）
package clipboard

// Board 系统剪贴板（文本 + 文件引用）
type Board interface {
	// Get 读当前剪贴板文本；无内容/不可用返回 ok=false
	Get() (text string, ok bool)
	// Set 写入文本并设为剪贴板内容；成功返回 true
	Set(text string) bool
	// SetFile 把本地文件的引用设为剪贴板内容（远程收到文件后 Ctrl+V 粘贴出文件）；
	// 平台不支持或失败返回 false（调用方自行降级为告知路径）
	SetFile(absPath string) bool
	// Close 释放底层资源（进程长存场景幂等）
	Close()
}

// Open 打开系统剪贴板；不支持/失败返回 nil,false
func Open() (Board, bool) { return open() }
