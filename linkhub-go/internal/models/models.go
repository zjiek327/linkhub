package models

import "time"

type DeviceGroup struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	ParentID  *int64    `json:"parent_id"`
	CreatedAt time.Time `json:"created_at"`
}

type Template struct {
	ID                 int64                  `json:"id"`
	Key                string                 `json:"key"`
	Name               string                 `json:"name"`
	Category           string                 `json:"category"`
	Icon               string                 `json:"icon"`
	Description        string                 `json:"description"`
	Spec               map[string]interface{} `json:"spec"`
	DefaultConnections []map[string]interface{} `json:"default_connections"`
	Builtin            bool                   `json:"builtin"`
	CreatedAt          time.Time              `json:"created_at"`
}

type Device struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Location    string    `json:"location"`
	Owner       string    `json:"owner"`
	Tags        []string  `json:"tags"`
	GroupID     *int64    `json:"group_id"`
	TemplateID  *int64    `json:"template_id"`
	NodeID      string    `json:"node_id"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	// 运行时填充
	Online     bool   `json:"online"`
	NodeName   string `json:"node_name,omitempty"`
	NodeOnline bool   `json:"node_online"`
}

type ConnectionProfile struct {
	ID           int64                  `json:"id"`
	DeviceID     int64                  `json:"device_id"`
	Kind         string                 `json:"kind"`
	Name         string                 `json:"name"`
	Params       map[string]interface{} `json:"params"`
	CredentialID *int64                 `json:"credential_id"`
	Enabled      bool                   `json:"enabled"`
	NodeID       string                 `json:"node_id"`
	CreatedAt    time.Time              `json:"created_at"`
}

type Credential struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	SecretEnc string    `json:"-"`
	CreatedAt time.Time `json:"created_at"`
}

type Session struct {
	ID           int64      `json:"id"`
	ConnectionID *int64     `json:"connection_id"`
	OpenedBy     string     `json:"opened_by"`
	OpenedAt     time.Time  `json:"opened_at"`
	ClosedAt     *time.Time `json:"closed_at"`
	Status       string     `json:"status"`
	LastError    string     `json:"last_error"`
	NodeID       string     `json:"node_id"`
	// JOIN 带出，终端 Tab 标题用
	ConnectionName string `json:"connection_name,omitempty"`
	DeviceName     string `json:"device_name,omitempty"`
}

type SessionLog struct {
	ID        int64     `json:"id"`
	SessionID int64     `json:"session_id"`
	Ts        time.Time `json:"ts"`
	Direction string    `json:"direction"`
	Data      string    `json:"data"`
}

type AuditLog struct {
	ID     int64     `json:"id"`
	User   string    `json:"user"`
	Action string    `json:"action"`
	Target string    `json:"target"`
	Detail string    `json:"detail"`
	Ts     time.Time `json:"ts"`
}

type User struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	PasswordHash string    `json:"-"`
	Role         string    `json:"role"`
	Enabled      bool      `json:"enabled"`
	CreatedAt    time.Time `json:"created_at"`
}

type Node struct {
	NodeID    string            `json:"node_id"`
	Name      string            `json:"name"`
	Address   string            `json:"address"`
	Status    string            `json:"status"`
	IsSelf    bool              `json:"is_self"`
	LastSeen  time.Time         `json:"last_seen"`
	Resources map[string]interface{} `json:"resources"`
	// Via 非空表示间接节点：经该 node_id 的直连 peer 中转可达（跨网段桥接）
	Via string `json:"via,omitempty"`
}

type ScheduledTask struct {
	ID        int64                  `json:"id"`
	Name      string                 `json:"name"`
	Command   string                 `json:"command"`
	Targets   []map[string]interface{} `json:"targets"`
	IntervalS int                    `json:"interval_s"`
	Enabled   bool                   `json:"enabled"`
	WaitMs    int                    `json:"wait_ms"`
	LastRun   *time.Time             `json:"last_run"`
	History   []map[string]interface{} `json:"history"`
	CreatedAt time.Time              `json:"created_at"`
}
