// Package desktop 节点桌面共享能力探测（VNC/RFB）。
// 三态：active=VNC 服务端已在监听可直接连；available=有图形会话但未起 VNC 服务（给安装指引）；
// headless=无图形环境不支持。探测结果带 TTL 缓存，供资源广播高频调用。
package desktop

import (
	"fmt"
	"net"
	"sync"
	"time"

	"linkhub/internal/desktop/capture"
)

type Capability struct {
	Status string `json:"status"` // builtin=进程内采集可用（零依赖） | active=VNC 在监听 | available=有图形会话未起 VNC | headless=无桌面
	Port   int    `json:"port"`
	Hint   string `json:"hint"` // available/headless 时的指引说明
}

var (
	cacheMu sync.Mutex
	cached  Capability
	cacheAt time.Time
)

const cacheTTL = 15 * time.Second

// Probe 探测本机桌面共享能力（15s 缓存，ResourceWatchLoop 2s 一轮不会真打端口/扫进程）
func Probe(port int) Capability {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if time.Since(cacheAt) < cacheTTL && cached.Port == port {
		return cached
	}
	c := probe(port)
	cached, cacheAt = c, time.Now()
	return c
}

func probe(port int) Capability {
	// 内置采集可用（Windows GDI / X11）：零第三方依赖，优先级最高
	if scr, ok := capture.Open(); ok {
		scr.Close()
		return Capability{Status: "builtin", Port: port, Hint: "内置采集（无需 VNC 服务端）"}
	}
	if portListening(port) {
		return Capability{Status: "active", Port: port}
	}
	if ok, hint := graphicalSession(); ok {
		return Capability{Status: "available", Port: port, Hint: hint}
	}
	return Capability{Status: "headless", Port: port, Hint: "该节点无图形桌面环境"}
}

func portListening(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 300*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// ---------- 远程安装辅助（「需要我帮你安装吗？」→「需要」） ----------
type SetupResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	Output  string `json:"output,omitempty"` // 安装命令输出（密码已脱敏）
}

// Setup 在目标节点安装并启动 VNC 服务端（平台实现见 setup_*.go）
func Setup(port int, password string) SetupResult {
	return setupDesktop(port, password)
}

// cacheReset 安装成功后立即清缓存，让下一次探测看到新状态
func cacheReset() {
	cacheMu.Lock()
	cacheAt = time.Time{}
	cacheMu.Unlock()
}
