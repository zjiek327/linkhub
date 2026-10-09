package ws

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/coder/websocket"

	"linkhub/internal/cluster"
	"linkhub/internal/session"
)

// Relay 处理跨节点终端中继：浏览器 → 本节点 → 归属节点
type RelayHandler struct {
	manager *session.Manager
	cluster *cluster.Cluster
}

func NewRelayHandler(mgr *session.Manager, cl *cluster.Cluster) *RelayHandler {
	return &RelayHandler{manager: mgr, cluster: cl}
}

// RelayTerminal 中继：/ws/terminal/{session_id}?node={node_id}
func (h *RelayHandler) RelayTerminal(w http.ResponseWriter, r *http.Request) {
	nodeID := r.URL.Query().Get("node")
	peer := h.cluster.GetPeer(nodeID)
	if peer == nil || peer.Status != "online" {
		wsClose(w, r, 4403, fmt.Sprintf("节点 %s 不在线", nodeID))
		return
	}
	// 浏览器 WS
	browser, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
	if err != nil {
		return
	}
	defer browser.Close(websocket.StatusNormalClosure, "")

	// 连归属节点的 relay 端点（带令牌）
	sid := r.PathValue("session_id")
	relayURL := fmt.Sprintf("ws://%s/ws/cluster/relay/%s?%s",
		peer.Address[len("http://"):], sid, r.URL.RawQuery)
	headers := http.Header{"X-LinkHub-Token": []string{h.cluster.Token()}}
	peerWS, _, err := websocket.Dial(r.Context(), relayURL, &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		browser.Close(websocket.StatusInternalError, "中继失败")
		return
	}
	defer peerWS.Close(websocket.StatusNormalClosure, "")

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// 双向管道
	go pipeWS(ctx, browser, peerWS)
	pipeWS(ctx, peerWS, browser)
}

func pipeWS(ctx context.Context, dst, src *websocket.Conn) {
	for {
		typ, data, err := src.Read(ctx)
		if err != nil {
			return
		}
		if err := dst.Write(ctx, typ, data); err != nil {
			return
		}
	}
}

// PeerRelay 归属节点的中继落点：/ws/cluster/relay/{session_id}
func (h *RelayHandler) PeerRelay(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-LinkHub-Token") != h.cluster.Token() {
		http.Error(w, "令牌无效", http.StatusForbidden)
		return
	}
	sid := r.PathValue("session_id")
	var id int64
	fmt.Sscanf(sid, "%d", &id)
	sess := h.manager.Get(id)
	if sess == nil {
		http.Error(w, "会话不存在", http.StatusNotFound)
		return
	}
	// 复用终端处理逻辑（本地管道）
	// 这里直接调 Terminal handler 的核心
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
	if err != nil {
		return
	}
	defer c.Close(websocket.StatusNormalClosure, "")
	// 简化为纯数据转发（协作控制在归属节点的 Session 里做）
	// 订阅会话并把数据泵给 peer
	// 客户端 ID 必须每次唯一：同一会话可有多个浏览器经中继旁观，
	// 固定 ID 会让 Subscribe 复用同一队列、双方画面互相串流
	relayCID := fmt.Sprintf("relay-%s-%d", sid, time.Now().UnixNano())
	client, isWriter := sess.Subscribe(relayCID, "中继")
	defer sess.Unsubscribe(relayCID)
	// 角色快照（对齐本地终端协议）
	c.Write(r.Context(), websocket.MessageText, []byte(jsonCtrl(map[string]interface{}{
		"type": "role", "writer": sess.WriterID, "me": relayCID,
	})))
	_ = isWriter
	go func() {
		for item := range client.Queue {
			if data, ok := item.([]byte); ok {
				c.Write(r.Context(), websocket.MessageBinary, data)
			} else if m, ok := item.(map[string]interface{}); ok {
				c.Write(r.Context(), websocket.MessageText, []byte(jsonCtrl(m)))
			}
		}
	}()
	for {
		typ, data, err := c.Read(r.Context())
		if err != nil {
			return
		}
		sess.Write("", data)
		_ = typ
	}
}

func urlQuery(u string) string {
	if q, err := url.ParseQuery(u); err == nil {
		return q.Encode()
	}
	return u
}
