package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
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
	// 直接用浏览器端 cid 作为会话客户端 ID：协作协议（role/input_request/grant_write）
	// 全部以 cid 为目标标识，中继保持同一 cid 才能让申请/授权流程透明穿透。
	// cid 由发起方 URL 带来（RelayTerminal 原样转发 query），浏览器标签页内唯一不会冲突；
	// 无 cid 时（对端旧版本）退化为唯一中继 ID
	relayCID := r.URL.Query().Get("cid")
	name := r.URL.Query().Get("name")
	if relayCID == "" {
		relayCID = fmt.Sprintf("relay-%s-%d", sid, time.Now().UnixNano())
	}
	if name == "" {
		name = "远端用户"
	}
	client, isWriter := sess.Subscribe(relayCID, name)
	defer sess.Unsubscribe(relayCID)
	_ = isWriter
	// 角色快照（对齐本地终端协议）
	c.Write(r.Context(), websocket.MessageText, []byte(jsonCtrl(map[string]interface{}{
		"type": "role", "writer": sess.WriterID, "me": relayCID,
	})))
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
		// 控制帧（申请/授权写入权等）交给会话协作逻辑，不能当设备数据写出去
		if typ == websocket.MessageText && strings.HasPrefix(string(data), `{"lh":`) {
			var body struct {
				LH map[string]interface{} `json:"lh"`
			}
			if json.Unmarshal(data, &body) == nil && body.LH != nil {
				sess.Control(relayCID, body.LH)
				continue
			}
		}
		// 按 cid 写入：非写入者会被会话拒绝（前端收到 input_blocked）
		sess.Write(relayCID, data)
	}
}

func urlQuery(u string) string {
	if q, err := url.ParseQuery(u); err == nil {
		return q.Encode()
	}
	return u
}
