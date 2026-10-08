package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"linkhub/internal/models"
)

// Store 是 SQLite 数据访问层
type Store struct {
	db *sql.DB
}

// Open 打开数据库并自动迁移
func Open(dbPath string) (*Store, error) {
	os.MkdirAll(filepath.Dir(dbPath), 0o755)
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(on)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := migrate(db); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func migrate(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT DEFAULT '');
	CREATE TABLE IF NOT EXISTS device_groups (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT UNIQUE NOT NULL,
		parent_id INTEGER REFERENCES device_groups(id),
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE IF NOT EXISTS templates (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		key TEXT UNIQUE NOT NULL,
		name TEXT NOT NULL,
		category TEXT DEFAULT '开发板',
		icon TEXT DEFAULT '🧩',
		description TEXT DEFAULT '',
		spec TEXT DEFAULT '{}',
		default_connections TEXT DEFAULT '[]',
		builtin BOOLEAN DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE IF NOT EXISTS devices (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		description TEXT DEFAULT '',
		location TEXT DEFAULT '',
		owner TEXT DEFAULT '',
		tags TEXT DEFAULT '[]',
		group_id INTEGER REFERENCES device_groups(id),
		template_id INTEGER REFERENCES templates(id),
		node_id TEXT DEFAULT 'local',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE IF NOT EXISTS credentials (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT UNIQUE NOT NULL,
		type TEXT DEFAULT 'password',
		secret_enc TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE IF NOT EXISTS connection_profiles (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		device_id INTEGER NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
		kind TEXT NOT NULL,
		name TEXT NOT NULL,
		params TEXT DEFAULT '{}',
		credential_id INTEGER REFERENCES credentials(id),
		enabled BOOLEAN DEFAULT 1,
		node_id TEXT DEFAULT 'local',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE IF NOT EXISTS sessions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		connection_id INTEGER REFERENCES connection_profiles(id),
		opened_by TEXT DEFAULT 'admin',
		opened_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		closed_at DATETIME,
		status TEXT DEFAULT 'connecting',
		last_error TEXT DEFAULT '',
		node_id TEXT DEFAULT 'local'
	);
	CREATE TABLE IF NOT EXISTS session_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id INTEGER NOT NULL REFERENCES sessions(id),
		ts DATETIME DEFAULT CURRENT_TIMESTAMP,
		direction TEXT NOT NULL,
		data TEXT DEFAULT ''
	);
	CREATE TABLE IF NOT EXISTS audit_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user TEXT DEFAULT 'admin',
		action TEXT NOT NULL,
		target TEXT DEFAULT '',
		detail TEXT DEFAULT '',
		ts DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE IF NOT EXISTS users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT UNIQUE NOT NULL,
		password_hash TEXT NOT NULL,
		role TEXT DEFAULT 'viewer',
		enabled BOOLEAN DEFAULT 1,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE IF NOT EXISTS nodes (
		node_id TEXT PRIMARY KEY,
		name TEXT DEFAULT '',
		address TEXT DEFAULT '',
		status TEXT DEFAULT 'online',
		is_self BOOLEAN DEFAULT 0,
		last_seen DATETIME DEFAULT CURRENT_TIMESTAMP,
		resources TEXT DEFAULT '{}'
	);
	CREATE TABLE IF NOT EXISTS scheduled_tasks (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		command TEXT NOT NULL,
		targets TEXT DEFAULT '[]',
		interval_s INTEGER DEFAULT 300,
		enabled BOOLEAN DEFAULT 1,
		wait_ms INTEGER DEFAULT 1500,
		last_run DATETIME,
		history TEXT DEFAULT '[]',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_devices_name ON devices(name);
	CREATE INDEX IF NOT EXISTS idx_conn_device ON connection_profiles(device_id);
	CREATE INDEX IF NOT EXISTS idx_sessions_conn ON sessions(connection_id);
	CREATE INDEX IF NOT EXISTS idx_logs_session ON session_logs(session_id);
	`
	_, err := db.Exec(schema)
	return err
}

// Exec 执行写操作，lastID 非空时回填
func (s *Store) Exec(lastID *int64, query string, args ...interface{}) error {
	r, err := s.db.Exec(query, args...)
	if err != nil {
		return err
	}
	if lastID != nil {
		*lastID, _ = r.LastInsertId()
	}
	return nil
}

// ---------- 设备 ----------
func (s *Store) ListDevices(keyword, tag string, groupID *int64) ([]models.Device, int, error) {
	q := `SELECT id,name,description,location,owner,tags,group_id,template_id,node_id,created_at,updated_at FROM devices`
	args := []interface{}{}
	where := ""
	if keyword != "" {
		where += " AND (name LIKE ? OR description LIKE ?)"
		args = append(args, "%"+keyword+"%", "%"+keyword+"%")
	}
	if groupID != nil {
		where += " AND group_id=?"
		args = append(args, *groupID)
	}
	if where != "" {
		q += " WHERE 1=1" + where
	}
	q += " ORDER BY id DESC"
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []models.Device{}
	for rows.Next() {
		var d models.Device
		var tags string
		var gid, tid sql.NullInt64
		if err := rows.Scan(&d.ID, &d.Name, &d.Description, &d.Location, &d.Owner,
			&tags, &gid, &tid, &d.NodeID, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, 0, err
		}
		json.Unmarshal([]byte(tags), &d.Tags)
		if gid.Valid {
			d.GroupID = &gid.Int64
		}
		if tid.Valid {
			d.TemplateID = &tid.Int64
		}
		out = append(out, d)
	}
	if tag != "" {
		filtered := out[:0]
		for _, d := range out {
			for _, t := range d.Tags {
				if t == tag {
					filtered = append(filtered, d)
					break
				}
			}
		}
		out = filtered
	}
	return out, len(out), nil
}

func (s *Store) GetDevice(id int64) (*models.Device, error) {
	var d models.Device
	var tags string
	var gid, tid sql.NullInt64
	err := s.db.QueryRow(`SELECT id,name,description,location,owner,tags,group_id,template_id,node_id,created_at,updated_at FROM devices WHERE id=?`, id).
		Scan(&d.ID, &d.Name, &d.Description, &d.Location, &d.Owner, &tags, &gid, &tid, &d.NodeID, &d.CreatedAt, &d.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(tags), &d.Tags)
	if gid.Valid {
		d.GroupID = &gid.Int64
	}
	if tid.Valid {
		d.TemplateID = &tid.Int64
	}
	return &d, nil
}

func (s *Store) CreateDevice(d *models.Device) error {
	tags, _ := json.Marshal(d.Tags)
	var id int64
	err := s.Exec(&id,
		`INSERT INTO devices (name,description,location,owner,tags,group_id,template_id,node_id) VALUES (?,?,?,?,?,?,?,?)`,
		d.Name, d.Description, d.Location, d.Owner, string(tags), d.GroupID, d.TemplateID, d.NodeID)
	if err != nil {
		return err
	}
	d.ID = id
	return nil
}

func (s *Store) UpdateDevice(d *models.Device) error {
	tags, _ := json.Marshal(d.Tags)
	return s.Exec(nil,
		`UPDATE devices SET name=?,description=?,location=?,owner=?,tags=?,group_id=?,updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		d.Name, d.Description, d.Location, d.Owner, string(tags), d.GroupID, d.ID)
}

func (s *Store) DeleteDevice(id int64) error {
	// 级联：连接配置 → 会话 → 日志
	var connIDs []int64
	rows, _ := s.db.Query(`SELECT id FROM connection_profiles WHERE device_id=?`, id)
	for rows.Next() {
		var cid int64
		rows.Scan(&cid)
		connIDs = append(connIDs, cid)
	}
	rows.Close()
	for _, cid := range connIDs {
		s.db.Exec(`DELETE FROM session_logs WHERE session_id IN (SELECT id FROM sessions WHERE connection_id=?)`, cid)
		s.db.Exec(`DELETE FROM sessions WHERE connection_id=?`, cid)
	}
	s.db.Exec(`DELETE FROM connection_profiles WHERE device_id=?`, id)
	return s.Exec(nil, `DELETE FROM devices WHERE id=?`, id)
}

// ---------- 分组 ----------
func (s *Store) ListGroups() ([]models.DeviceGroup, error) {
	rows, err := s.db.Query(`SELECT id,name,parent_id,created_at FROM device_groups ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.DeviceGroup{}
	for rows.Next() {
		var g models.DeviceGroup
		var pid sql.NullInt64
		if err := rows.Scan(&g.ID, &g.Name, &pid, &g.CreatedAt); err != nil {
			return nil, err
		}
		if pid.Valid {
			g.ParentID = &pid.Int64
		}
		out = append(out, g)
	}
	return out, nil
}

func (s *Store) CreateGroup(name string, parentID *int64) (*models.DeviceGroup, error) {
	var id int64
	err := s.Exec(&id, `INSERT INTO device_groups (name, parent_id) VALUES (?, ?)`, name, parentID)
	if err != nil {
		return nil, err
	}
	return &models.DeviceGroup{ID: id, Name: name, ParentID: parentID}, nil
}

// ---------- 模板 ----------
func (s *Store) ListTemplates() ([]models.Template, error) {
	rows, err := s.db.Query(`SELECT id,key,name,category,icon,description,spec,default_connections,builtin,created_at FROM templates ORDER BY builtin DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Template{}
	for rows.Next() {
		var t models.Template
		var spec, dc string
		if err := rows.Scan(&t.ID, &t.Key, &t.Name, &t.Category, &t.Icon, &t.Description, &spec, &dc, &t.Builtin, &t.CreatedAt); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(spec), &t.Spec)
		json.Unmarshal([]byte(dc), &t.DefaultConnections)
		out = append(out, t)
	}
	return out, nil
}

func (s *Store) GetTemplateByKey(key string) (*models.Template, error) {
	var t models.Template
	var spec, dc string
	err := s.db.QueryRow(`SELECT id,key,name,category,icon,description,spec,default_connections,builtin,created_at FROM templates WHERE key=?`, key).
		Scan(&t.ID, &t.Key, &t.Name, &t.Category, &t.Icon, &t.Description, &spec, &dc, &t.Builtin, &t.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(spec), &t.Spec)
	json.Unmarshal([]byte(dc), &t.DefaultConnections)
	return &t, nil
}

func (s *Store) UpsertTemplate(t *models.Template) error {
	spec, _ := json.Marshal(t.Spec)
	dc, _ := json.Marshal(t.DefaultConnections)
	_, err := s.db.Exec(`INSERT INTO templates (key,name,category,icon,description,spec,default_connections,builtin)
		VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT(key) DO UPDATE SET name=excluded.name, category=excluded.category,
		  icon=excluded.icon, description=excluded.description, spec=excluded.spec,
		  default_connections=excluded.default_connections, builtin=excluded.builtin`,
		t.Key, t.Name, t.Category, t.Icon, t.Description, string(spec), string(dc), t.Builtin)
	return err
}

// ---------- 连接配置 ----------
func (s *Store) ListConnections(deviceID int64) ([]models.ConnectionProfile, error) {
	rows, err := s.db.Query(`SELECT id,device_id,kind,name,params,credential_id,enabled,node_id,created_at FROM connection_profiles WHERE device_id=?`, deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.ConnectionProfile{}
	for rows.Next() {
		var c models.ConnectionProfile
		var params string
		var credID sql.NullInt64
		if err := rows.Scan(&c.ID, &c.DeviceID, &c.Kind, &c.Name, &params, &credID, &c.Enabled, &c.NodeID, &c.CreatedAt); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(params), &c.Params)
		if credID.Valid {
			c.CredentialID = &credID.Int64
		}
		out = append(out, c)
	}
	return out, nil
}

func (s *Store) GetConnection(id int64) (*models.ConnectionProfile, error) {
	var c models.ConnectionProfile
	var params string
	var credID sql.NullInt64
	err := s.db.QueryRow(`SELECT id,device_id,kind,name,params,credential_id,enabled,node_id,created_at FROM connection_profiles WHERE id=?`, id).
		Scan(&c.ID, &c.DeviceID, &c.Kind, &c.Name, &params, &credID, &c.Enabled, &c.NodeID, &c.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(params), &c.Params)
	if credID.Valid {
		c.CredentialID = &credID.Int64
	}
	return &c, nil
}

func (s *Store) CreateConnection(c *models.ConnectionProfile) error {
	params, _ := json.Marshal(c.Params)
	var id int64
	err := s.Exec(&id,
		`INSERT INTO connection_profiles (device_id,kind,name,params,credential_id,enabled,node_id) VALUES (?,?,?,?,?,?,?)`,
		c.DeviceID, c.Kind, c.Name, string(params), c.CredentialID, c.Enabled, c.NodeID)
	if err != nil {
		return err
	}
	c.ID = id
	return nil
}

func (s *Store) DeleteConnection(id int64) error {
	s.db.Exec(`DELETE FROM session_logs WHERE session_id IN (SELECT id FROM sessions WHERE connection_id=?)`, id)
	s.db.Exec(`DELETE FROM sessions WHERE connection_id=?`, id)
	return s.Exec(nil, `DELETE FROM connection_profiles WHERE id=?`, id)
}

// ---------- 会话 ----------
func (s *Store) ListSessions(status string) ([]models.Session, error) {
	q := `SELECT id,connection_id,opened_by,opened_at,closed_at,status,last_error,node_id FROM sessions`
	args := []interface{}{}
	if status != "" {
		q += " WHERE status=?"
		args = append(args, status)
	}
	q += " ORDER BY id DESC"
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Session{}
	for rows.Next() {
		var ss models.Session
		var connID sql.NullInt64
		var closedAt sql.NullTime
		if err := rows.Scan(&ss.ID, &connID, &ss.OpenedBy, &ss.OpenedAt, &closedAt, &ss.Status, &ss.LastError, &ss.NodeID); err != nil {
			return nil, err
		}
		if connID.Valid {
			ss.ConnectionID = &connID.Int64
		}
		if closedAt.Valid {
			ss.ClosedAt = &closedAt.Time
		}
		out = append(out, ss)
	}
	return out, nil
}

func (s *Store) GetSession(id int64) (*models.Session, error) {
	var ss models.Session
	var connID sql.NullInt64
	var closedAt sql.NullTime
	err := s.db.QueryRow(`SELECT id,connection_id,opened_by,opened_at,closed_at,status,last_error,node_id FROM sessions WHERE id=?`, id).
		Scan(&ss.ID, &connID, &ss.OpenedBy, &ss.OpenedAt, &closedAt, &ss.Status, &ss.LastError, &ss.NodeID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if connID.Valid {
		ss.ConnectionID = &connID.Int64
	}
	if closedAt.Valid {
		ss.ClosedAt = &closedAt.Time
	}
	return &ss, nil
}

func (s *Store) SessionLogs(sessionID int64, afterID int64, limit int) ([]models.SessionLog, error) {
	rows, err := s.db.Query(`SELECT id,session_id,ts,direction,data FROM session_logs WHERE session_id=? AND id>? ORDER BY id LIMIT ?`,
		sessionID, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.SessionLog{}
	for rows.Next() {
		var l models.SessionLog
		if err := rows.Scan(&l.ID, &l.SessionID, &l.Ts, &l.Direction, &l.Data); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, nil
}

// ---------- 用户 ----------
func (s *Store) GetUserByName(name string) (*models.User, error) {
	var u models.User
	err := s.db.QueryRow(`SELECT id,name,password_hash,role,enabled,created_at FROM users WHERE name=?`, name).
		Scan(&u.ID, &u.Name, &u.PasswordHash, &u.Role, &u.Enabled, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &u, err
}

func (s *Store) CreateUser(u *models.User) error {
	var id int64
	err := s.Exec(&id, `INSERT INTO users (name,password_hash,role,enabled) VALUES (?,?,?,?)`,
		u.Name, u.PasswordHash, u.Role, u.Enabled)
	if err != nil {
		return err
	}
	u.ID = id
	return nil
}

func (s *Store) ListUsers() ([]models.User, error) {
	rows, err := s.db.Query(`SELECT id,name,role,enabled,created_at FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.User{}
	for rows.Next() {
		var u models.User
		rows.Scan(&u.ID, &u.Name, &u.Role, &u.Enabled, &u.CreatedAt)
		out = append(out, u)
	}
	return out, nil
}

func (s *Store) Audit(user, action, target, detail string) {
	s.db.Exec(`INSERT INTO audit_logs (user,action,target,detail) VALUES (?,?,?,?)`, user, action, target, detail)
}

func (s *Store) ListAudit(limit int) ([]models.AuditLog, error) {
	rows, err := s.db.Query(`SELECT id,user,action,target,detail,ts FROM audit_logs ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.AuditLog{}
	for rows.Next() {
		var a models.AuditLog
		rows.Scan(&a.ID, &a.User, &a.Action, &a.Target, &a.Detail, &a.Ts)
		out = append(out, a)
	}
	return out, nil
}

// ---------- 节点 ----------
func (s *Store) ListNodes() ([]models.Node, error) {
	rows, err := s.db.Query(`SELECT node_id,name,address,status,is_self,last_seen,resources FROM nodes ORDER BY is_self DESC, node_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Node{}
	for rows.Next() {
		var n models.Node
		var res string
		rows.Scan(&n.NodeID, &n.Name, &n.Address, &n.Status, &n.IsSelf, &n.LastSeen, &res)
		json.Unmarshal([]byte(res), &n.Resources)
		out = append(out, n)
	}
	return out, nil
}

func (s *Store) UpsertNode(n *models.Node) error {
	res, _ := json.Marshal(n.Resources)
	_, err := s.db.Exec(`INSERT INTO nodes (node_id,name,address,status,is_self,last_seen,resources) VALUES (?,?,?,?,?,?,?)
		ON CONFLICT(node_id) DO UPDATE SET name=excluded.name,address=excluded.address,status=excluded.status,last_seen=excluded.last_seen,resources=excluded.resources`,
		n.NodeID, n.Name, n.Address, n.Status, n.IsSelf, n.LastSeen.Format(time.RFC3339), string(res))
	return err
}

func (s *Store) DeleteNode(nodeID string) error {
	return s.Exec(nil, `DELETE FROM nodes WHERE node_id=?`, nodeID)
}

func (s *Store) GetMeta(key string) string {
	var v string
	s.db.QueryRow(`SELECT value FROM meta WHERE key=?`, key).Scan(&v)
	return v
}

func (s *Store) SetMeta(key, value string) error {
	return s.Exec(nil, `INSERT INTO meta (key,value) VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
}

// ---------- 凭证 ----------
func (s *Store) ListCredentials() ([]models.Credential, error) {
	rows, err := s.db.Query(`SELECT id,name,type,created_at FROM credentials ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Credential{}
	for rows.Next() {
		var c models.Credential
		rows.Scan(&c.ID, &c.Name, &c.Type, &c.CreatedAt)
		out = append(out, c)
	}
	return out, nil
}

func (s *Store) CreateCredential(name, typ, secretEnc string) (*models.Credential, error) {
	var id int64
	err := s.Exec(&id, `INSERT INTO credentials (name,type,secret_enc) VALUES (?,?,?)`, name, typ, secretEnc)
	return &models.Credential{ID: id, Name: name, Type: typ}, err
}

func (s *Store) GetCredential(id int64) (*models.Credential, error) {
	var c models.Credential
	err := s.db.QueryRow(`SELECT id,name,type,secret_enc,created_at FROM credentials WHERE id=?`, id).
		Scan(&c.ID, &c.Name, &c.Type, &c.SecretEnc, &c.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &c, err
}

// ---------- 定时任务 ----------
func (s *Store) ListTasks() ([]models.ScheduledTask, error) {
	rows, err := s.db.Query(`SELECT id,name,command,targets,interval_s,enabled,wait_ms,last_run,history,created_at FROM scheduled_tasks ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.ScheduledTask{}
	for rows.Next() {
		var t models.ScheduledTask
		var targets, history string
		var lastRun sql.NullTime
		rows.Scan(&t.ID, &t.Name, &t.Command, &targets, &t.IntervalS, &t.Enabled, &t.WaitMs, &lastRun, &history, &t.CreatedAt)
		json.Unmarshal([]byte(targets), &t.Targets)
		json.Unmarshal([]byte(history), &t.History)
		if lastRun.Valid {
			t.LastRun = &lastRun.Time
		}
		out = append(out, t)
	}
	return out, nil
}

func (s *Store) CreateTask(t *models.ScheduledTask) error {
	targets, _ := json.Marshal(t.Targets)
	var id int64
	err := s.Exec(&id, `INSERT INTO scheduled_tasks (name,command,targets,interval_s,enabled,wait_ms) VALUES (?,?,?,?,?,?)`,
		t.Name, t.Command, string(targets), t.IntervalS, t.Enabled, t.WaitMs)
	t.ID = id
	return err
}

func (s *Store) DeleteTask(id int64) error {
	return s.Exec(nil, `DELETE FROM scheduled_tasks WHERE id=?`, id)
}

func fmtErr(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%v", err)
}
