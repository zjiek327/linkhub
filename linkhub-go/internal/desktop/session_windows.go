//go:build windows

package desktop

// graphicalSession Windows 桌面/服务器（非 Core）总有控制台图形会话
func graphicalSession() (bool, string) {
	return true, "安装并启动 TightVNC/UltraVNC 服务端（监听 5900）后即可共享桌面"
}
