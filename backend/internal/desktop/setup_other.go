//go:build !linux

package desktop

// setupDesktop 非 Linux 节点暂不支持自动安装
func setupDesktop(port int, password string) SetupResult {
	return SetupResult{Message: "该系统暂不支持自动安装：Windows 请手动安装 TightVNC/UltraVNC 服务端（监听 5900 并设置密码）后重试"}
}
