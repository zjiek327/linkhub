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
