package session

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"time"

	"linkhub/internal/connector"
	"linkhub/internal/store"
)

var maskAfter = [][]byte{[]byte("assword:"), []byte("PIN:")}

type Client struct {
	ID      string
	Name    string
	Queue   chan interface{} // []byte 终端数据 | map 控制消息
	Writer  bool
}

type Session struct {
	ID        int64
	DeviceID  int64
	ConnID    *int64
	Kind      string
	Params    map[string]interface{}
	Connector connector.Connector
	Status    string
	LastError string
	WriterID  string
	// Done 会话终止（被关闭）后关闭，WS 处理器据此断开客户端
	Done chan struct{}

	mu       sync.RWMutex
	clients  map[string]*Client
	chats    []map[string]interface{} // 会话内聊天记录（仅内存，随会话消失）
	closing  bool
	ctx      context.Context
	cancel   context.CancelFunc
	store    *store.Store
	masked   bool
}

type Manager struct {
	mu       sync.RWMutex
	sessions map[int64]*Session
	portLock map[string]int64 // 端口锁 → 会话 ID
	store    *store.Store
	// OnStatus 状态变更钩子（事件总线用），main 里注入
	OnStatus func(sessionID, deviceID int64, status, errMsg string)
}

func NewManager(st *store.Store) *Manager {
	return &Manager{sessions: map[int64]*Session{}, portLock: map[string]int64{}, store: st}
}

func (m *Manager) lockKey(kind string, params map[string]interface{}) string {
	port, _ := params["port"].(string)
	if port == "" {
		port = "?"
	}
	return kind + ":" + port
}

// PortBusyError 端口已有活跃会话：携带持有会话 ID，调用方应让后来者加入该会话（共享旁观）
type PortBusyError struct {
	Message string
	Holder  int64
}

func (e *PortBusyError) Error() string { return e.Message }

// Open 打开会话（或返回端口占用冲突）
func (m *Manager) Open(connID int64, kind string, params map[string]interface{}, deviceID int64, openedBy string) (*Session, error) {
	m.mu.Lock()
	key := m.lockKey(kind, params)
	if holder, ok := m.portLock[key]; ok {
		s := m.sessions[holder]
		m.mu.Unlock()
		return s, &PortBusyError{Message: fmt.Sprintf("端口已被会话 %d 占用", holder), Holder: holder}
	}
	m.mu.Unlock()

	// 落库（connection_id 允许 NULL：内联会话/测试）
	var sessID int64
	var connIDVal interface{}
	if connID > 0 {
		connIDVal = connID
	}
	err := m.store.Exec(&sessID,
		`INSERT INTO sessions (connection_id, opened_by, status, node_id) VALUES (?, ?, 'connecting', 'local')`,
		connIDVal, openedBy)
	if err != nil {
		return nil, err
	}

	conn, err := connector.Create(kind, params)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Session{
		ID: sessID, DeviceID: deviceID, ConnID: &connID, Kind: kind, Params: params,
		Connector: conn, Status: "connecting", Done: make(chan struct{}),
		clients: map[string]*Client{}, ctx: ctx, cancel: cancel, store: m.store,
	}
	m.mu.Lock()
	m.sessions[sessID] = s
	m.portLock[key] = sessID
	m.mu.Unlock()

	go s.run(m)
	return s, nil
}

func (m *Manager) Get(id int64) *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[id]
}

func (m *Manager) Close(id int64) error {
	m.mu.Lock()
	s, ok := m.sessions[id]
	if ok {
		delete(m.sessions, id)
		delete(m.portLock, m.lockKey(s.Kind, s.Params))
	}
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("会话不存在")
	}
	s.cancel()
	s.Connector.Close() // 尽快释放端口（run 循环退出时还会兜底关一次）
	// 通知所有 WS 客户端（本地/中继）：会话已结束，处理器据此断开并带原因
	s.mu.Lock()
	if !s.closing {
		s.closing = true
		close(s.Done)
	}
	s.broadcastCtrlLocked(map[string]interface{}{
		"type": "session_status", "session_id": s.ID, "device_id": s.DeviceID, "status": "closed",
	})
	s.mu.Unlock()
	m.store.Exec(nil, `UPDATE sessions SET closed_at=CURRENT_TIMESTAMP, status='closed' WHERE id=?`, id)
	return nil
}

func (m *Manager) OnlineDeviceIDs() map[int64]bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := map[int64]bool{}
	for _, s := range m.sessions {
		if s.Status == "online" {
			out[s.DeviceID] = true
		}
	}
	return out
}

func (m *Manager) OnlineSessionIDs() map[int64]bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := map[int64]bool{}
	for id, s := range m.sessions {
		if s.Status == "online" {
			out[id] = true
		}
	}
	return out
}

// ---------- 会话运行循环 ----------
func (s *Session) run(m *Manager) {
	delay := time.Second
	defer func() {
		// 退出必关连接器，否则串口句柄泄漏（独占打开后无法再用）
		s.Connector.Close()
	}()
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}
		s.setStatus(m, "connecting", "")
		if err := s.openWithParams(); err != nil {
			s.setStatus(m, "error", err.Error())
			if !s.autoReconnect() {
				m.Close(s.ID)
				return
			}
			time.Sleep(delay)
			delay = min(delay*2, 30*time.Second)
			continue
		}
		s.setStatus(m, "online", "")
		delay = time.Second
		if err := s.pump(m); err != nil {
			s.setStatus(m, "error", err.Error())
			s.Connector.Close()
			if !s.autoReconnect() {
				m.Close(s.ID)
				return
			}
			time.Sleep(delay)
			delay = min(delay*2, 30*time.Second)
		}
	}
}

func (s *Session) openWithParams() error {
	if sc, ok := s.Connector.(*connector.SerialConnector); ok {
		return sc.OpenWithParams(s.Params)
	}
	return s.Connector.Open(s.ctx)
}

func (s *Session) autoReconnect() bool {
	v, _ := s.Params["auto_reconnect"].(bool)
	return v
}

func (s *Session) pump(m *Manager) error {
	r, err := s.Connector.Read()
	if err != nil {
		return err
	}
	buf := make([]byte, 4096)
	for {
		select {
		case <-s.ctx.Done():
			return nil
		default:
		}
		n, err := r.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			s.maybeMask(chunk)
			s.broadcast(chunk)
			s.log(m, "rx", chunk)
		}
		if err != nil {
			return fmt.Errorf("读取中断: %w", err)
		}
	}
}

func (s *Session) maybeMask(chunk []byte) {
	tail := chunk
	if len(tail) > 32 {
		tail = tail[len(tail)-32:]
	}
	for _, p := range maskAfter {
		if bytes.Contains(tail, p) {
			s.mu.Lock()
			s.masked = true
			s.mu.Unlock()
			return
		}
	}
}

func (s *Session) broadcast(data []byte) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	s.broadcastData(data)
}

// broadcastData 向所有客户端推数据（调用方必须已持有 s.mu 读锁或写锁）
func (s *Session) broadcastData(data []byte) {
	for _, c := range s.clients {
		select {
		case c.Queue <- data:
		default: // 慢消费者丢帧
		}
	}
}

func (s *Session) broadcastCtrl(msg map[string]interface{}) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	s.broadcastCtrlLocked(msg)
}

// broadcastCtrlLocked 控制消息广播（调用方必须已持有 s.mu 读锁或写锁；
// 注意 RWMutex 不可重入，Subscribe/Control 等持写锁路径必须用 Locked 版本）
func (s *Session) broadcastCtrlLocked(msg map[string]interface{}) {
	for _, c := range s.clients {
		select {
		case c.Queue <- msg:
		default:
		}
	}
}

func (s *Session) log(m *Manager, dir string, data []byte) {
	st := s.store
	if st == nil && m != nil {
		st = m.store
	}
	if st == nil {
		return
	}
	st.Exec(nil,
		`INSERT INTO session_logs (session_id, direction, data) VALUES (?, ?, ?)`,
		s.ID, dir, base64.StdEncoding.EncodeToString(data))
}

func (s *Session) setStatus(m *Manager, status, errMsg string) {
	s.mu.Lock()
	s.Status, s.LastError = status, errMsg
	s.mu.Unlock()
	m.store.Exec(nil, `UPDATE sessions SET status=?, last_error=? WHERE id=?`, status, errMsg, s.ID)
	s.broadcastCtrl(map[string]interface{}{
		"type": "session_status", "session_id": s.ID, "device_id": s.DeviceID,
		"status": status, "error": errMsg,
	})
	if m.OnStatus != nil {
		m.OnStatus(s.ID, s.DeviceID, status, errMsg)
	}
}

// ---------- 写入与协作 ----------
func (s *Session) Write(clientID string, data []byte) (bool, error) {
	s.mu.RLock()
	writer := s.WriterID
	s.mu.RUnlock()
	if clientID != "" && writer != "" && clientID != writer {
		return false, nil
	}
	if err := s.Connector.Write(data); err != nil {
		return false, err
	}
	logData := data
	if s.masked {
		logData = []byte("******")
	}
	s.log(nil, "tx", logData)
	if bytes.HasSuffix(data, []byte("\n")) || bytes.Equal(data, []byte("\r")) {
		s.mu.Lock()
		s.masked = false
		s.mu.Unlock()
	}
	return true, nil
}

func (s *Session) Subscribe(clientID, name string) (*Client, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.clients[clientID]; ok {
		return c, c.Writer
	}
	c := &Client{ID: clientID, Name: name, Queue: make(chan interface{}, 512)}
	if s.WriterID == "" || s.WriterID == clientID {
		s.WriterID = clientID
		c.Writer = true
	}
	s.clients[clientID] = c
	// 补发会话聊天历史（新加入的旁观者能看到之前聊了什么）
	for _, m := range s.chats {
		select {
		case c.Queue <- m:
		default:
		}
	}
	s.broadcastCtrlLocked(map[string]interface{}{"type": "presence", "viewers": len(s.clients)})
	return c, c.Writer
}

func (s *Session) Unsubscribe(clientID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.clients, clientID)
	if s.WriterID == clientID {
		s.WriterID = ""
		s.broadcastCtrlLocked(map[string]interface{}{"type": "writer_free"})
	}
	s.broadcastCtrlLocked(map[string]interface{}{"type": "presence", "viewers": len(s.clients)})
}

func (s *Session) Control(clientID string, msg map[string]interface{}) {
	typ, _ := msg["type"].(string)
	// chat：会话内协作聊天，广播给所有客户端（写入者+旁观者，本地+中继皆可发）
	if typ == "chat" {
		text, _ := msg["text"].(string)
		text = strings.TrimSpace(text)
		if text == "" || len(text) > 500 {
			return
		}
		s.mu.Lock()
		name := "?"
		if c, ok := s.clients[clientID]; ok && c.Name != "" {
			name = c.Name
		}
		out := map[string]interface{}{
			"type": "chat", "from": clientID, "name": name,
			"text": text, "ts": time.Now().Format("15:04:05"),
		}
		s.chats = append(s.chats, out)
		if len(s.chats) > 50 {
			s.chats = s.chats[len(s.chats)-50:]
		}
		s.broadcastCtrlLocked(out)
		s.mu.Unlock()
		return
	}
	// resize：仅写入者可改窗口尺寸，避免旁观者互相拉扯
	if typ == "resize" {
		rows, _ := msg["rows"].(float64)
		cols, _ := msg["cols"].(float64)
		if rows < 2 || cols < 2 || rows > 500 || cols > 1000 {
			return
		}
		s.mu.RLock()
		allowed := s.WriterID == "" || s.WriterID == clientID
		s.mu.RUnlock()
		if allowed {
			s.Connector.Resize(int(rows), int(cols))
		}
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch typ {
	case "request_write":
		if s.WriterID == "" {
			s.WriterID = clientID
			if c, ok := s.clients[clientID]; ok {
				c.Writer = true
			}
			s.broadcastCtrlLocked(map[string]interface{}{"type": "role_change", "writer": clientID, "writer_name": s.clients[clientID].Name})
		} else {
			s.broadcastCtrlLocked(map[string]interface{}{"type": "input_request", "from": clientID, "name": msg["name"]})
		}
	case "grant_write":
		if s.WriterID == clientID {
			target, _ := msg["to"].(string)
			if c, ok := s.clients[target]; ok {
				for _, cc := range s.clients {
					cc.Writer = false
				}
				c.Writer = true
				s.WriterID = target
				s.broadcastCtrlLocked(map[string]interface{}{"type": "role_change", "writer": target, "writer_name": c.Name})
			}
		}
	case "deny_write":
		if s.WriterID == clientID {
			s.broadcastCtrlLocked(map[string]interface{}{"type": "input_denied", "to": msg["to"]})
		}
	case "release_write":
		if s.WriterID == clientID {
			s.WriterID = ""
			s.broadcastCtrlLocked(map[string]interface{}{"type": "writer_free"})
		}
	}
}
