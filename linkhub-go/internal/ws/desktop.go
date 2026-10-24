package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"

	"linkhub/internal/cluster"
	"linkhub/internal/desktop"
	"linkhub/internal/desktop/capture"
	"linkhub/internal/desktop/input"
)

// DesktopHandler 节点桌面共享：/ws/desktop?node=<id>
// 本机 → WS⇄TCP 代理到本机 VNC 端口；跨节点 → 复用集群路由中继（X-Relay-Hops 限 3 跳）。
// 鉴权：浏览器 JWT（query token）；节点间中继用 X-LinkHub-Token（各节点 JWT 密钥不同，不能透传）。
type DesktopHandler struct {
	Port    int
	Cluster *cluster.Cluster // nil = 集群未启用，仅本机
	Auth    func(*http.Request) bool
}

// Capability GET /api/desktop/capability（路由层已挂 auth 中间件）
func (h *DesktopHandler) Capability(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(desktop.Probe(h.Port))
}

// Setup POST /api/desktop/setup {node, password}（路由层已挂 auth 中间件）
// 本机直接执行安装；远程节点经集群路由转发到对端 internal 端点执行
func (h *DesktopHandler) Setup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Node     string `json:"node"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "请求体错误", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if body.Node == "" || h.Cluster == nil || body.Node == h.Cluster.SelfID() {
		json.NewEncoder(w).Encode(desktop.Setup(h.Port, body.Password))
		return
	}
	var out desktop.SetupResult
	if err := h.Cluster.CallAny(body.Node, "POST", "/api/cluster/internal/desktop/setup",
		map[string]string{"password": body.Password}, &out); err != nil {
		json.NewEncoder(w).Encode(desktop.SetupResult{Message: "转发到目标节点失败: " + err.Error()})
		return
	}
	json.NewEncoder(w).Encode(out)
}

func (h *DesktopHandler) WS(w http.ResponseWriter, r *http.Request) {
	// 中继链路（带集群令牌）跳过 JWT；浏览器直连必须 JWT
	if h.Cluster == nil || r.Header.Get("X-LinkHub-Token") != h.Cluster.Token() {
		if h.Auth != nil && !h.Auth(r) {
			wsClose(w, r, 4401, "未登录或登录已过期")
			return
		}
	}
	node := r.URL.Query().Get("node")
	if node == "" || h.Cluster == nil || node == h.Cluster.SelfID() {
		h.localDesktop(w, r)
		return
	}
	h.relayDesktop(w, r, node)
}

// localDesktop 本机桌面：builtin → 进程内采集流；active → VNC 代理
func (h *DesktopHandler) localDesktop(w http.ResponseWriter, r *http.Request) {
	cap := desktop.Probe(h.Port)
	if cap.Status == "builtin" {
		h.localBuiltin(w, r)
		return
	}
	if cap.Status == "headless" {
		wsClose(w, r, 4403, "该节点无图形桌面环境")
		return
	}
	h.localVNC(w, r)
}

// localBuiltin 内置采集会话：下行 JPEG 脏矩形帧（二进制），上行输入事件（JSON 文本帧）
func (h *DesktopHandler) localBuiltin(w http.ResponseWriter, r *http.Request) {
	scr, ok := capture.Open()
	if !ok {
		wsClose(w, r, 4403, "内置采集不可用")
		return
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
	if err != nil {
		scr.Close()
		return
	}
	defer c.Close(websocket.StatusNormalClosure, "")

	stream := capture.NewStream(scr, 12)
	sz := scr.Size()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// 下行：采集循环 → WS
	go func() {
		defer cancel()
		if err := stream.Run(func(frame []byte) {
			if werr := c.Write(ctx, websocket.MessageBinary, frame); werr != nil {
				return
			}
		}); err != nil {
			log.Printf("[desktop-builtin] 采集循环退出: %v", err)
		}
	}()
	defer stream.Stop()

	// 上行：输入事件 → 平台注入
	inj, hasInput := input.Open()
	if inj != nil {
		defer inj.Close()
	}
	if !hasInput {
		inj = nil
	}
	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			continue
		}
		var ev struct {
			Type    string `json:"type"` // mouse_move | mouse_button | wheel | key
			X, Y    int    `json:"x,y"`
			Button  int    `json:"button"`
			Down    bool   `json:"down"`
			Dy      int    `json:"dy"`
			Keysym  uint32 `json:"keysym"`
		}
		if json.Unmarshal(data, &ev) != nil || inj == nil {
			continue
		}
		switch ev.Type {
		case "mouse_move":
			inj.MouseMove(ev.X, ev.Y)
		case "mouse_button":
			inj.MouseButton(ev.Button, ev.Down)
		case "wheel":
			inj.Wheel(ev.Dy)
		case "key":
			inj.Key(ev.Keysym, ev.Down)
		}
	}
	_ = sz
}

// localVNC 本机 VNC 代理：浏览器 WS ⇄ 127.0.0.1:port
func (h *DesktopHandler) localVNC(w http.ResponseWriter, r *http.Request) {
	cap := desktop.Probe(h.Port)
	if cap.Status == "headless" {
		wsClose(w, r, 4403, "该节点无图形桌面环境")
		return
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
	if err != nil {
		return
	}
	defer c.Close(websocket.StatusNormalClosure, "")

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", h.Port), 5*time.Second)
	if err != nil {
		c.Close(websocket.StatusInternalError, fmt.Sprintf("VNC 服务端不可达（127.0.0.1:%d）", h.Port))
		return
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	// 下行：VNC → 浏览器（二进制帧）
	go func() {
		defer cancel()
		buf := make([]byte, 64*1024)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				if werr := c.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	// 上行：浏览器 → VNC（文本帧也转发，RFB 全是二进制，容错）
	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		_ = typ
		if _, err := conn.Write(data); err != nil {
			return
		}
	}
}

// relayDesktop 跨节点中继：浏览器 WS ⇄ 下一跳节点的 /ws/desktop（逐跳原样传 node）
func (h *DesktopHandler) relayDesktop(w http.ResponseWriter, r *http.Request, node string) {
	hops, _ := strconv.Atoi(r.Header.Get("X-Relay-Hops"))
	if hops > 3 {
		wsClose(w, r, 4403, "中继跳数超限")
		return
	}
	peer, indirect, ok := h.Cluster.ResolveRoute(node)
	if !ok || peer.Status != "online" {
		wsClose(w, r, 4403, fmt.Sprintf("节点 %s 不在线或不可达", node))
		return
	}
	browser, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
	if err != nil {
		return
	}
	defer browser.Close(websocket.StatusNormalClosure, "")

	relayURL := fmt.Sprintf("ws://%s/ws/desktop?%s",
		peer.Address[len("http://"):], r.URL.RawQuery)
	headers := http.Header{"X-LinkHub-Token": []string{h.Cluster.Token()}}
	if indirect {
		headers.Set("X-Relay-Hops", strconv.Itoa(hops+1))
	}
	peerWS, _, err := websocket.Dial(r.Context(), relayURL, &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		browser.Close(websocket.StatusInternalError, "中继失败")
		return
	}
	defer peerWS.Close(websocket.StatusNormalClosure, "")

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go pipeWS(ctx, browser, peerWS)
	pipeWS(ctx, peerWS, browser)
}
