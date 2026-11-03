// Package capture 内置屏幕采集（零第三方依赖的桌面共享数据源）。
// 协议帧格式（WS 二进制下行）：
//
//	[1]  msg=1 帧头 [2B 屏宽][2B 屏高][2B 脏块数]
//	    每块: [2B x][2B y][2B w][2B h][4B jpegLen][jpeg...]
//
// 输入事件走 JSON 文本帧（见 ws/desktop.go handleDesktopInput）。
package capture

import "image"

// Screen 内置采集会话（由平台实现提供）
type Screen interface {
	// Size 当前屏幕尺寸
	Size() image.Point
	// Frame 抓一帧全屏 RGBA（top-down），调用方持有缓冲
	Frame(buf []byte) ([]byte, error)
	// Close 释放资源
	Close()
}

// Open 打开平台采集器（windows / linux+x11；其余返回 ok=false → 走 VNC 模式）
func Open() (Screen, bool) { return open() }

// AlternateFor 返回 s 的备用采集源（无则 nil）。Stream 在连续帧错误时切换：
// Windows 锁屏切安全桌面后 Mag 窗口停止合成必失败，回退 GDI（黑帧+光标，
// 鼠标仍可见可控），解锁后错误消失自动切回首选源
func AlternateFor(s Screen) Screen { return alternateFor(s) }
