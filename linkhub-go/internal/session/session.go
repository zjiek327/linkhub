package session

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
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

	mu       sync.RWMutex
	clients  map[string]*Client
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

// Open 打开会话（或返回端口占用冲突）
func (m *Manager) Open(connID int64, kind string, params map[string]interface{}, deviceID int64, openedBy string) (*Session, error) {
	m.mu.Lock()
	key := m.lockKey(kind, params)
	if holder, ok := m.portLock[key]; ok {
		m.mu.Unlock()
		return m.sessions[holder], fmt.Errorf("端口已被会话 %d 占用", holder)
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
		Connector: conn, Status: "connecting",
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
	for _, c := range s.clients {
		select {
		case c.Queue <- msg:
		default:
		}
	}
}

func (s *Session) log(m *Manager, dir string, data []byte) {
	m.store.Exec(nil,
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
	s.broadcastCtrl(map[string]interface{}{"type": "presence", "viewers": len(s.clients)})
	return c, c.Writer
}

func (s *Session) Unsubscribe(clientID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.clients, clientID)
	if s.WriterID == clientID {
		s.WriterID = ""
		s.broadcastCtrl(map[string]interface{}{"type": "writer_free"})
	}
	s.broadcastCtrl(map[string]interface{}{"type": "presence", "viewers": len(s.clients)})
}

func (s *Session) Control(clientID string, msg map[string]interface{}) {
	typ, _ := msg["type"].(string)
	s.mu.Lock()
	defer s.mu.Unlock()
	switch typ {
	case "request_write":
		if s.WriterID == "" {
			s.WriterID = clientID
			if c, ok := s.clients[clientID]; ok {
				c.Writer = true
			}
			s.broadcastCtrl(map[string]interface{}{"type": "role_change", "writer": clientID, "writer_name": s.clients[clientID].Name})
		} else {
			s.broadcastCtrl(map[string]interface{}{"type": "input_request", "from": clientID, "name": msg["name"]})
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
				s.broadcastCtrl(map[string]interface{}{"type": "role_change", "writer": target, "writer_name": c.Name})
			}
		}
	case "deny_write":
		if s.WriterID == clientID {
			s.broadcastCtrl(map[string]interface{}{"type": "input_denied", "to": msg["to"]})
		}
	case "release_write":
		if s.WriterID == clientID {
			s.WriterID = ""
			s.broadcastCtrl(map[string]interface{}{"type": "writer_free"})
		}
	}
}
