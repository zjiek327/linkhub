package store

import (
	"database/sql"
	"encoding/json"
	"time"

	"linkhub/internal/models"
)

// ---------- 连接配置 ----------
func (s *Store) UpdateConnection(c *models.ConnectionProfile) error {
	params, _ := json.Marshal(c.Params)
	return s.Exec(nil,
		`UPDATE connection_profiles SET kind=?, name=?, params=?, credential_id=?, enabled=?, node_id=? WHERE id=?`,
		c.Kind, c.Name, string(params), c.CredentialID, c.Enabled, c.NodeID, c.ID)
}

// ConnectionsOfDevice 返回设备的连接配置（ListConnections 别名，语义清晰）
func (s *Store) ConnectionsOfDevice(deviceID int64) ([]models.ConnectionProfile, error) {
	return s.ListConnections(deviceID)
}

// ---------- 用户 ----------
func (s *Store) GetUser(id int64) (*models.User, error) {
	var u models.User
	err := s.db.QueryRow(`SELECT id,name,password_hash,role,enabled,created_at FROM users WHERE id=?`, id).
		Scan(&u.ID, &u.Name, &u.PasswordHash, &u.Role, &u.Enabled, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *Store) UpdateUser(u *models.User) error {
	return s.Exec(nil, `UPDATE users SET name=?, password_hash=?, role=?, enabled=? WHERE id=?`,
		u.Name, u.PasswordHash, u.Role, u.Enabled, u.ID)
}

func (s *Store) DeleteUser(id int64) error {
	return s.Exec(nil, `DELETE FROM users WHERE id=?`, id)
}

// ---------- 凭证 ----------
func (s *Store) DeleteCredential(id int64) error {
	return s.Exec(nil, `DELETE FROM credentials WHERE id=?`, id)
}

// ---------- 定时任务 ----------
func (s *Store) GetTask(id int64) (*models.ScheduledTask, error) {
	tasks, err := s.ListTasks()
	if err != nil {
		return nil, err
	}
	for _, t := range tasks {
		if t.ID == id {
			return &t, nil
		}
	}
	return nil, nil
}

// UpdateTask 全量更新（含调度用的 last_run/history 与可编辑字段）
func (s *Store) UpdateTask(t *models.ScheduledTask) error {
	targets, _ := json.Marshal(t.Targets)
	history, _ := json.Marshal(t.History)
	var lastRun interface{}
	if t.LastRun != nil {
		lastRun = t.LastRun.Format(time.RFC3339)
	}
	_, err := s.db.Exec(`UPDATE scheduled_tasks SET name=?, command=?, targets=?, interval_s=?, enabled=?, wait_ms=?, last_run=?, history=? WHERE id=?`,
		t.Name, t.Command, string(targets), t.IntervalS, t.Enabled, t.WaitMs, lastRun, string(history), t.ID)
	return err
}

// ---------- 会话 ----------
// MarkAllSessionsClosed 启动时把上次残留的"开启"会话标记关闭（防僵尸）
func (s *Store) MarkAllSessionsClosed() {
	s.Exec(nil, `UPDATE sessions SET status='closed', closed_at=CURRENT_TIMESTAMP WHERE status IN ('connecting','online','error')`)
}

// SessionIDsByConnection 某连接配置下的所有会话 ID
func (s *Store) SessionIDsByConnection(connID int64) []int64 {
	rows, err := s.db.Query(`SELECT id FROM sessions WHERE connection_id=?`, connID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		out = append(out, id)
	}
	return out
}

// SessionLogTexts 导出用：按顺序取日志（direction + base64 data）
func (s *Store) AllSessionLogs(sessionID int64) ([]models.SessionLog, error) {
	return s.SessionLogs(sessionID, 0, 1000000)
}
