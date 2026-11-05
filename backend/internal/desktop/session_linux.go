//go:build linux

package desktop

import (
	"os"
	"path/filepath"
	"strings"
)

// graphicalSession 是否有活跃图形会话（Wayland socket / X11 socket / 常见桌面进程）
func graphicalSession() (bool, string) {
	// Wayland 优先：Wayland 会话必然同时存在 Xwayland 的 X socket，
	// 先查 X11 会把 Wayland 机器误判成 X11
	if m, _ := filepath.Glob("/run/user/*/wayland-*"); len(m) > 0 {
		return true, "检测到 Wayland 图形会话：当前版本不支持自动抓屏。请注销后选「Ubuntu on Xorg」登录（GDM 点用户名后右下角齿轮），或用 Windows 节点体验桌面共享"
	}
	if m, _ := filepath.Glob("/tmp/.X11-unix/X*"); len(m) > 0 {
		return true, "检测到 X11 图形会话：安装并运行 x11vnc（如 x11vnc -display :0 -forever -passwd <密码>）即可共享当前桌面"
	}
	// 兜底：扫进程名（Xorg/gnome-shell/kwin/sway/weston/xfce 等）
	names := []string{"Xorg", "X", "gnome-shell", "kwin_wayland", "kwin_x11", "sway", "weston", "xfce4-session", "lxqt-session", "cinnamon", "mate-session"}
	if entries, err := filepath.Glob("/proc/[0-9]*/comm"); err == nil {
		for _, e := range entries {
			b, err := os.ReadFile(e)
			if err != nil {
				continue
			}
			comm := strings.TrimSpace(string(b))
			for _, n := range names {
				if comm == n {
					return true, "检测到图形会话（" + comm + "）：安装 VNC 服务端（x11vnc / TigerVNC / wayvnc）后即可共享"
				}
			}
		}
	}
	return false, ""
}
