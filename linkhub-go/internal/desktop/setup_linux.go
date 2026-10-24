//go:build linux

package desktop

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// setupDesktop Linux 自动安装：仅支持 X11 会话（x11vnc）。
// 需要免密 sudo（sudo -n）；Wayland 与无桌面节点返回手动指引。
func setupDesktop(port int, password string) SetupResult {
	if strings.TrimSpace(password) == "" {
		return SetupResult{Message: "请先设置 VNC 访问密码"}
	}
	if portListening(port) {
		return SetupResult{OK: true, Message: "VNC 服务端已在运行，可直接连接"}
	}
	m, _ := filepath.Glob("/tmp/.X11-unix/X*")
	if len(m) == 0 {
		return SetupResult{Message: "仅 X11 会话支持自动安装。Wayland：GNOME 46+ 在「设置 → 共享 → 桌面共享」开 VNC 模式，其他合成器用 wayvnc；无桌面节点不支持"}
	}
	disp := ":" + strings.TrimPrefix(filepath.Base(m[0]), "X")

	var out strings.Builder
	if _, err := exec.LookPath("x11vnc"); err != nil {
		out.WriteString("$ sudo -n apt-get install -y x11vnc\n")
		b, err := exec.Command("sudo", "-n", "apt-get", "install", "-y", "x11vnc").CombinedOutput()
		out.Write(b)
		if err != nil {
			return SetupResult{Message: "自动安装 x11vnc 失败（需要免密 sudo，或手动执行 sudo apt install -y x11vnc 后重试）", Output: out.String()}
		}
	}
	// -bg：x11vnc 初始化后自行转后台；-auth guess 自动找 X 授权
	logFile := fmt.Sprintf("/tmp/linkhub-x11vnc-%d.log", port)
	out.WriteString(fmt.Sprintf("$ x11vnc -display %s -forever -shared -auth guess -rfbport %d -passwd ****** -bg -o %s\n", disp, port, logFile))
	b, err := exec.Command("x11vnc", "-display", disp, "-forever", "-shared", "-auth", "guess",
		"-rfbport", fmt.Sprint(port), "-passwd", password, "-bg", "-o", logFile).CombinedOutput()
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
