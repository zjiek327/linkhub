package ws

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/coder/websocket"

	"linkhub/internal/events"
	"linkhub/internal/session"
)

func NewHandler(mgr *session.Manager, bus *events.Bus) *Handler {
	return &Handler{manager: mgr, bus: bus}
}

type Handler struct {
	manager *session.Manager
	bus     *events.Bus
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
	if err := c.Write(r.Context(), websocket.MessageText, []byte(jsonCtrl(map[string]interface{}{
		"type": "role", "writer": sess.WriterID, "me": clientID,
	}))); err != nil {
		return
	}

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
// 每条事件为一行 JSON 文本帧：{"event":"session_status","data":{...}}
func (h *Handler) Events(w http.ResponseWriter, r *http.Request) {
	if h.bus == nil {
		wsClose(w, r, 1011, "事件总线未启用")
		return
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
	if err != nil {
		return
	}
	defer c.Close(websocket.StatusNormalClosure, "")

	ch, unsub := h.bus.Subscribe()
	defer unsub()
	ctx := r.Context()
	// 读泵：只为感知客户端断开
	go func() {
		for {
			if _, _, err := c.Read(ctx); err != nil {
				return
			}
		}
	}()
	for ev := range ch {
		b, err := json.Marshal(ev)
		if err != nil {
			continue
		}
		if err := c.Write(ctx, websocket.MessageText, b); err != nil {
			return
		}
	}
}

func logf(format string, args ...interface{}) { log.Printf(format, args...) }
