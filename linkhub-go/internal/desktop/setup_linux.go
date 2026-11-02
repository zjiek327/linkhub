//go:build linux

package desktop

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// findXAuth 按显示管理器探测 X 授权 cookie 文件（x11vnc 以 root 运行时
// -auth guess 只找 /root/.Xauthority 和 FD_XDM，找不到会话用户的 cookie）
func findXAuth(xsocket string) string {
	disp := ":" + strings.TrimPrefix(filepath.Base(xsocket), "X")
	candidates := []string{
		"/var/run/lightdm/root/" + disp, // LightDM
		"/var/lib/gdm3/" + disp + ".auth", // 旧版 GDM
	}
	if g, _ := filepath.Glob("/run/user/*/gdm/Xauthority"); len(g) > 0 {
		candidates = append(candidates, g...) // GDM（GNOME/Ubuntu 默认）
	}
	if g, _ := filepath.Glob("/run/user/*/.Xauthority"); len(g) > 0 {
		candidates = append(candidates, g...)
	}
	if g, _ := filepath.Glob("/home/*/.Xauthority"); len(g) > 0 {
		candidates = append(candidates, g...)
	}
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c
		}
	}
	return ""
}

// setupDesktop Linux 自动安装：仅支持 X11 会话（x11vnc）。
// 需要免密 sudo（sudo -n）；Wayland 与无桌面节点返回手动指引。
func setupDesktop(port int, password string) SetupResult {
	if strings.TrimSpace(password) == "" {
		return SetupResult{Message: "请先设置 VNC 访问密码"}
	}
	if portListening(port) {
		return SetupResult{OK: true, Message: "VNC 服务端已在运行，可直接连接"}
	}
	// Wayland 会话前置检测：/tmp/.X11-unix 里的 socket 都是 Xwayland 的（rootless），
	// x11vnc 连上也只能采到空根窗口（黑屏）；guess 授权同样找不到 mutter 的随机 cookie。
	if wl, _ := filepath.Glob("/run/user/*/wayland-*"); len(wl) > 0 {
		return SetupResult{Message: "当前桌面会话是 Wayland，x11vnc 与内置采集都无法抓屏。请注销后重新登录，在登录界面选择「Ubuntu on Xorg」（GDM 点用户名后右下角齿轮切换），登录成功后再点设置"}
	}
	m, _ := filepath.Glob("/tmp/.X11-unix/X*")
	if len(m) == 0 {
		return SetupResult{Message: "未检测到图形会话。无桌面节点不支持桌面共享"}
	}
	disp := ":" + strings.TrimPrefix(filepath.Base(m[0]), "X")

	// -auth：guess 在 root 下找不到会话用户的 cookie（/root/.Xauthority 不存在），
	// 按显示管理器逐个探测候选授权文件
	auth := findXAuth(m[0])
	authArg := auth
	if authArg == "" {
		authArg = "guess"
	}

	var out strings.Builder
	if _, err := exec.LookPath("x11vnc"); err != nil {
		out.WriteString("$ sudo -n apt-get install -y x11vnc\n")
		b, err := exec.Command("sudo", "-n", "apt-get", "install", "-y", "x11vnc").CombinedOutput()
		out.Write(b)
		if err != nil {
			return SetupResult{Message: "自动安装 x11vnc 失败（需要免密 sudo，或手动执行 sudo apt install -y x11vnc 后重试）", Output: out.String()}
		}
	}
	// -bg：x11vnc 初始化后自行转后台
	logFile := fmt.Sprintf("/tmp/linkhub-x11vnc-%d.log", port)
	out.WriteString(fmt.Sprintf("$ x11vnc -display %s -forever -shared -auth %s -rfbport %d -passwd ****** -bg -o %s\n", disp, authArg, port, logFile))
	cmd := exec.Command("x11vnc", "-display", disp, "-forever", "-shared", "-auth", authArg,
		"-rfbport", fmt.Sprint(port), "-passwd", password, "-bg", "-o", logFile)
	if auth != "" {
		cmd.Env = append(os.Environ(), "XAUTHORITY="+auth)
	}
	b, err := cmd.CombinedOutput()
	out.Write(b)
	if err != nil {
		return SetupResult{Message: "x11vnc 启动失败，详见输出", Output: out.String()}
	}
	for i := 0; i < 30; i++ {
		if portListening(port) {
			cacheReset()
			return SetupResult{OK: true, Message: fmt.Sprintf("x11vnc 已启动（display %s，端口 %d），可用刚才的密码连接", disp, port), Output: out.String()}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return SetupResult{Message: "x11vnc 已执行但端口未就绪，详见输出", Output: out.String()}
}
