package store

import (
	"database/sql"
	"linkhub/internal/models"
)

// ---------- 租户 ----------
func (s *Store) ListTenants() ([]models.Tenant, error) {
	rows, err := s.db.Query(`SELECT id,name,code,status,remark,created_at FROM tenants ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Tenant{}
	for rows.Next() {
		var t models.Tenant
		if err := rows.Scan(&t.ID, &t.Name, &t.Code, &t.Status, &t.Remark, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

func (s *Store) GetTenant(id int64) (*models.Tenant, error) {
	var t models.Tenant
	err := s.db.QueryRow(`SELECT id,name,code,status,remark,created_at FROM tenants WHERE id=?`, id).
		Scan(&t.ID, &t.Name, &t.Code, &t.Status, &t.Remark, &t.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *Store) GetTenantByCode(code string) (*models.Tenant, error) {
	var t models.Tenant
	err := s.db.QueryRow(`SELECT id,name,code,status,remark,created_at FROM tenants WHERE code=?`, code).
		Scan(&t.ID, &t.Name, &t.Code, &t.Status, &t.Remark, &t.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *Store) CreateTenant(t *models.Tenant) error {
	var id int64
	err := s.Exec(&id, `INSERT INTO tenants (name, code, status, remark) VALUES (?,?,?,?)`,
		t.Name, t.Code, orDefault(t.Status, "active"), t.Remark)
	if err != nil {
		return err
	}
	t.ID = id
	return nil
}

func (s *Store) UpdateTenant(t *models.Tenant) error {
	return s.Exec(nil, `UPDATE tenants SET name=?, code=?, status=?, remark=? WHERE id=?`,
		t.Name, t.Code, t.Status, t.Remark, t.ID)
}

func (s *Store) DeleteTenant(id int64) error {
	if id == 1 {
		return errProtected
	}
	s.db.Exec(`DELETE FROM tenant_members WHERE tenant_id=?`, id)
	s.db.Exec(`DELETE FROM tenant_nodes WHERE tenant_id=?`, id)
	return s.Exec(nil, `DELETE FROM tenants WHERE id=?`, id)
}

// ---------- 成员 ----------
// MembersOfTenant 租户成员列表
func (s *Store) MembersOfTenant(tenantID int64) ([]models.TenantMember, error) {
	rows, err := s.db.Query(`SELECT m.id, m.tenant_id, m.user_id, m.role, m.created_at, u.name
		FROM tenant_members m JOIN users u ON m.user_id=u.id WHERE m.tenant_id=? ORDER BY m.id`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.TenantMember{}
	for rows.Next() {
		var m models.TenantMember
		if err := rows.Scan(&m.ID, &m.TenantID, &m.UserID, &m.Role, &m.CreatedAt, &m.UserName); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

// TenantsOfUser 用户的租户列表（登录选择/切换用）
func (s *Store) TenantsOfUser(userID int64) ([]models.TenantMember, error) {
	rows, err := s.db.Query(`SELECT m.id, m.tenant_id, m.role, m.created_at, t.name, t.code
		FROM tenant_members m JOIN tenants t ON m.tenant_id=t.id
		WHERE m.user_id=? AND t.status='active' ORDER BY m.tenant_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.TenantMember{}
	for rows.Next() {
		var m models.TenantMember
		if err := rows.Scan(&m.ID, &m.TenantID, &m.Role, &m.CreatedAt, &m.TenantName, &m.TenantCode); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

// GetMembership 某用户在某租户的角色；不存在返回 nil
func (s *Store) GetMembership(tenantID, userID int64) (*models.TenantMember, error) {
	var m models.TenantMember
	err := s.db.QueryRow(`SELECT id, tenant_id, user_id, role, created_at FROM tenant_members WHERE tenant_id=? AND user_id=?`,
		tenantID, userID).Scan(&m.ID, &m.TenantID, &m.UserID, &m.Role, &m.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// IsPlatformAdmin 平台管理员（跨租户）——当前约定：名为 admin 的账号
func (s *Store) IsPlatformAdmin(userID int64) bool {
	var name string
	if err := s.db.QueryRow(`SELECT name FROM users WHERE id=?`, userID).Scan(&name); err != nil {
		return false
	}
	return name == "admin"
}

func (s *Store) AddMember(tenantID, userID int64, role string) error {
	return s.Exec(nil, `INSERT OR IGNORE INTO tenant_members (tenant_id, user_id, role) VALUES (?,?,?)`,
		tenantID, userID, role)
}

func (s *Store) UpdateMemberRole(tenantID, userID int64, role string) error {
	return s.Exec(nil, `UPDATE tenant_members SET role=? WHERE tenant_id=? AND user_id=?`, role, tenantID, userID)
}

func (s *Store) RemoveMember(tenantID, userID int64) error {
	return s.Exec(nil, `DELETE FROM tenant_members WHERE tenant_id=? AND user_id=?`, tenantID, userID)
}

// ---------- 租户节点白名单 ----------
func (s *Store) TenantNodes(tenantID int64) []string {
	rows, err := s.db.Query(`SELECT node_id FROM tenant_nodes WHERE tenant_id=?`, tenantID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		rows.Scan(&n)
		out = append(out, n)
	}
	return out
}

func (s *Store) SetTenantNodes(tenantID int64, nodeIDs []string) error {
	if _, err := s.db.Exec(`DELETE FROM tenant_nodes WHERE tenant_id=?`, tenantID); err != nil {
		return err
	}
	for _, n := range nodeIDs {
		if _, err := s.db.Exec(`INSERT OR IGNORE INTO tenant_nodes (tenant_id, node_id) VALUES (?,?)`, tenantID, n); err != nil {
			return err
		}
	}
	return nil
}

// TenantAllowedNode 是否允许租户使用该节点（无白名单=全部允许）
func (s *Store) TenantAllowedNode(tenantID int64, nodeID string) bool {
	if nodeID == "" || nodeID == "local" {
		return true
	}
	list := s.TenantNodes(tenantID)
	if len(list) == 0 {
		return true
	}
	for _, n := range list {
		if n == nodeID {
			return true
		}
	}
	return false
}

var errProtected = &protectedError{}

type protectedError struct{}

func (*protectedError) Error() string { return "默认租户不可删除" }

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
