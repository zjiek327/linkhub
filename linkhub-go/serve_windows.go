//go:build windows

package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"syscall"

	"golang.org/x/sys/windows"
)

// soExclusiveAddrUse 独占地址选项。Windows 下 Go 默认给监听器设 SO_REUSEADDR，
// 而它允许第二个进程绑定同一端口——两实例静默共存、连接被随机分流，
// 表现为会话飘忽、间歇失控（schtasks /End 杀掉一个才恢复）。
// 设独占后第二个实例绑定直接失败，杜绝双实例：
// 报错信息里带上清理指引（taskkill / schtasks /End）
const soExclusiveAddrUse = 0xFFFB // winsock SO_EXCLUSIVEADDRUSE

func serveHTTP(addr string, h http.Handler) error {
	lc := net.ListenConfig{
		Control: func(_, _ string, c syscall.RawConn) error {
			var serr error
			if err := c.Control(func(fd uintptr) {
				serr = windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, soExclusiveAddrUse, 1)
			}); err != nil {
				return err
			}
			return serr
		},
	}
	ln, err := lc.Listen(context.Background(), "tcp", addr)
	if err != nil {
		return fmt.Errorf("监听 %s 失败: %v（若提示端口被占用：先 taskkill /F /IM linkhub.exe 或 schtasks /End /TN \"LinkHub\" 清理残留实例再启动）", addr, err)
	}
	log.Printf("🐙 灵枢 LinkHub 启动 http://0.0.0.0%s", addr)
	return http.Serve(ln, h)
}
