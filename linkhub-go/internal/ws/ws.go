package ws

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/coder/websocket"

	"linkhub/internal/session"
)

type Handler struct {
	manager *session.Manager
}

func NewHandler(mgr *session.Manager) *Handler {
	return &Handler{manager: mgr}
}

// Terminal 处理 WS 终端：/ws/terminal/{session_id}
func (h *Handler) Terminal(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("session_id")
	var sid int64
	fmt.Sscanf(id, "%d", &sid)
	sess := h.manager.Get(sid)
	if sess == nil {
		wsClose(w, r, 4404, "会话不存在或已关闭")
		return
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	})
	if err != nil {
		return
	}
	defer c.Close(websocket.StatusNormalClosure, "")

	clientID := r.URL.Query().Get("cid")
	name := r.URL.Query().Get("name")
	client, _ := sess.Subscribe(clientID, name)
	defer sess.Unsubscribe(clientID)

	// 先发角色快照
	c.Write(r.Context(), websocket.MessageText, []byte(jsonCtrl(map[string]interface{}{
		"type": "role", "writer": sess.WriterID, "me": clientID,
	})))

	ctx := r.Context()
	// 下行：设备/控制消息 → 浏览器
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case item, ok := <-client.Queue:
				if !ok {
					return
				}
				switch v := item.(type) {
				case []byte:
					c.Write(ctx, websocket.MessageBinary, v)
				case map[string]interface{}:
					c.Write(ctx, websocket.MessageText, []byte(jsonCtrl(v)))
				}
			}
		}
	}()

	// 上行：浏览器 → 设备/控制
	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		if typ == websocket.MessageText && strings.HasPrefix(string(data), `{"lh":`) {
			var body struct {
				LH map[string]interface{} `json:"lh"`
			}
			if json.Unmarshal(data, &body) == nil && body.LH != nil {
				sess.Control(clientID, body.LH)
				continue
			}
		}
		written, err := sess.Write(clientID, data)
		if err != nil {
			return
		}
		if !written {
			c.Write(ctx, websocket.MessageText, []byte(jsonCtrl(map[string]interface{}{"type": "input_blocked"})))
		}
	}
}

func jsonCtrl(v map[string]interface{}) string {
	b, _ := json.Marshal(map[string]interface{}{"lh": v})
	return string(b)
}

func wsClose(w http.ResponseWriter, r *http.Request, code int, reason string) {
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	c.Close(websocket.StatusCode(code), reason)
}

// Events 处理全局事件推送：/ws/events
func (h *Handler) Events(w http.ResponseWriter, r *http.Request) {
	// TODO: 事件总线
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
	if err != nil {
		return
	}
	defer c.Close(websocket.StatusNormalClosure, "")
	// 先保持连接，心跳由客户端 ping
	<-r.Context().Done()
}

func logf(format string, args ...interface{}) { log.Printf(format, args...) }
