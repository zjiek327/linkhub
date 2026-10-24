//go:build linux

package desktop

import (
	"os"
	"path/filepath"
	"strings"
)

// graphicalSession 是否有活跃图形会话（X11 socket / Wayland socket / 常见桌面进程）
func graphicalSession() (bool, string) {
	if m, _ := filepath.Glob("/tmp/.X11-unix/X*"); len(m) > 0 {
		return true, "检测到 X11 图形会话：安装并运行 x11vnc（如 x11vnc -display :0 -forever -passwd <密码>）即可共享当前桌面"
	}
	if m, _ := filepath.Glob("/run/user/*/wayland-*"); len(m) > 0 {
		return true, "检测到 Wayland 图形会话：GNOME 46+ 可在「设置 → 共享 → 桌面共享」启用 VNC 模式；其他合成器可用 wayvnc"
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
