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

// ---------- 设备组成员/设备 ----------
// GroupMembers 组内用户
func (s *Store) GroupMembers(groupID int64) ([]int64, error) {
	rows, err := s.db.Query(`SELECT user_id FROM device_group_members WHERE group_id=?`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		out = append(out, id)
	}
	return out, nil
}

// AddGroupMember / RemoveGroupMember
func (s *Store) AddGroupMember(groupID, userID int64) error {
	return s.Exec(nil, `INSERT OR IGNORE INTO device_group_members (group_id, user_id) VALUES (?,?)`, groupID, userID)
}

func (s *Store) RemoveGroupMember(groupID, userID int64) error {
	return s.Exec(nil, `DELETE FROM device_group_members WHERE group_id=? AND user_id=?`, groupID, userID)
}

// GroupsOfUser 用户所在的组（含租户过滤）
func (s *Store) GroupsOfUser(userID int64) ([]models.DeviceGroup, error) {
	rows, err := s.db.Query(`SELECT g.id, g.name, g.parent_id, g.created_at
		FROM device_groups g JOIN device_group_members m ON m.group_id=g.id
		WHERE m.user_id=? ORDER BY g.id`, userID)
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

// UserVisibility 分别返回：组授权可见设备（跨租户）、自己创建的设备（限租户）
func (s *Store) UserVisibility(userID int64, userName string, tenantID *int64) (map[int64]bool, map[int64]bool, error) {
	groupVisible := map[int64]bool{}
	q := `SELECT dgd.device_id FROM device_group_devices dgd
		JOIN device_group_members dgm ON dgm.group_id=dgd.group_id
		WHERE dgm.user_id=?`
	rows, err := s.db.Query(q, userID)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		groupVisible[id] = true
	}
	rows.Close()
	ownVisible := map[int64]bool{}
	q2 := `SELECT id FROM devices WHERE owner=?`
	args2 := []interface{}{userName}
	if tenantID != nil {
		q2 += " AND tenant_id=?"
		args2 = append(args2, *tenantID)
	}
	rows2, err := s.db.Query(q2, args2...)
	if err != nil {
		return nil, nil, err
	}
	defer rows2.Close()
	for rows2.Next() {
		var id int64
		rows2.Scan(&id)
		ownVisible[id] = true
	}
	return groupVisible, ownVisible, nil
}

// UserVisibleDeviceIDs 用户可见设备：所在组的设备 ∪ 自己创建的（owner=用户名）
func (s *Store) UserVisibleDeviceIDs(userID int64, userName string, tenantID *int64) (map[int64]bool, error) {
	out := map[int64]bool{}
	q := `SELECT dgd.device_id FROM device_group_devices dgd
		JOIN device_group_members dgm ON dgm.group_id=dgd.group_id
		JOIN devices d ON d.id=dgd.device_id
		WHERE dgm.user_id=?`
	args := []interface{}{userID}
	if tenantID != nil {
		q += " AND d.tenant_id=?"
		args = append(args, *tenantID)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		out[id] = true
	}
	rows.Close()
	// 自己创建的
	q2 := `SELECT id FROM devices WHERE owner=?`
	args2 := []interface{}{userName}
	if tenantID != nil {
		q2 += " AND tenant_id=?"
		args2 = append(args2, *tenantID)
	}
	rows2, err := s.db.Query(q2, args2...)
	if err != nil {
		return nil, err
	}
	defer rows2.Close()
	for rows2.Next() {
		var id int64
		rows2.Scan(&id)
		out[id] = true
	}
	return out, nil
}

// GroupDevices 组内设备
func (s *Store) GroupDevices(groupID int64) []int64 {
	rows, err := s.db.Query(`SELECT device_id FROM device_group_devices WHERE group_id=?`, groupID)
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

// SetGroupDevices 全量设置组内设备
func (s *Store) SetGroupDevices(groupID int64, deviceIDs []int64) error {
	if _, err := s.db.Exec(`DELETE FROM device_group_devices WHERE group_id=?`, groupID); err != nil {
		return err
	}
	for _, d := range deviceIDs {
		if _, err := s.db.Exec(`INSERT OR IGNORE INTO device_group_devices (group_id, device_id) VALUES (?,?)`, groupID, d); err != nil {
			return err
		}
	}
	return nil
}

// DevicesOfGroups 指定组的设备 ID 集
func (s *Store) DevicesOfGroups(groupIDs []int64) (map[int64]bool, error) {
	out := map[int64]bool{}
	if len(groupIDs) == 0 {
		return out, nil
	}
	q := `SELECT device_id FROM device_group_devices WHERE group_id IN (` + placeholders(len(groupIDs)) + `)`
	args := make([]interface{}, len(groupIDs))
	for i, id := range groupIDs {
		args[i] = id
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		out[id] = true
	}
	return out, nil
}

func placeholders(n int) string {
	out := ""
	for i := 0; i < n; i++ {
		if i > 0 {
			out += ","
		}
		out += "?"
	}
	return out
}

// ---------- 组共享（节点可见性） ----------
// CreateGroupFull 建组（归属节点 + 可见性）
func (s *Store) CreateGroupFull(name, nodeID, visibility string) (*models.DeviceGroup, error) {
	var id int64
	if visibility == "" {
		visibility = "private"
	}
	err := s.Exec(&id, `INSERT INTO device_groups (name, node_id, visibility) VALUES (?,?,?)`, name, nodeID, visibility)
	if err != nil {
		return nil, err
	}
	return &models.DeviceGroup{ID: id, Name: name, NodeID: nodeID, Visibility: visibility}, nil
}

// SetGroupVisibility 公开/私密 + 公开时的只读开关
func (s *Store) SetGroupVisibility(groupID int64, visibility string, readonly bool) error {
	return s.Exec(nil, `UPDATE device_groups SET visibility=?, readonly=? WHERE id=?`, visibility, readonly, groupID)
}

// SetDeviceAuthorizedNodes 私密组内设备的例外授权节点
func (s *Store) SetDeviceAuthorizedNodes(deviceID int64, grants []struct {
	NodeID   string
	Readonly bool
}) error {
	if _, err := s.db.Exec(`DELETE FROM device_authorized_nodes WHERE device_id=?`, deviceID); err != nil {
		return err
	}
	for _, g := range grants {
		if _, err := s.db.Exec(`INSERT OR IGNORE INTO device_authorized_nodes (device_id, node_id, readonly) VALUES (?,?,?)`,
			deviceID, g.NodeID, g.Readonly); err != nil {
			return err
		}
	}
	return nil
}

// DeviceGrant 设备对某节点的授权（含只读粒度）
type DeviceGrant struct {
	NodeID   string
	Readonly bool
}

// DeviceGrants 设备授权明细
func (s *Store) DeviceGrants(deviceID int64) []DeviceGrant {
	rows, err := s.db.Query(`SELECT node_id, readonly FROM device_authorized_nodes WHERE device_id=?`, deviceID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []DeviceGrant
	for rows.Next() {
		var g DeviceGrant
		rows.Scan(&g.NodeID, &g.Readonly)
		out = append(out, g)
	}
	return out
}

// DeviceAuthorizedNodes 设备的例外授权节点列表
func (s *Store) DeviceAuthorizedNodes(deviceID int64) []string {
	rows, err := s.db.Query(`SELECT node_id FROM device_authorized_nodes WHERE device_id=?`, deviceID)
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

// DeviceVisibility 计算设备的跨节点可见性与只读：
// 任一所属组公开 → public；public 且所有公开组都开了只读 → readonly=true
func (s *Store) DeviceVisibility(deviceID int64) (string, bool) {
	var pubAll, pubReadonly int
	s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(CASE WHEN g.readonly THEN 1 ELSE 0 END),0)
		FROM device_group_devices dgd JOIN device_groups g ON g.id=dgd.group_id
		WHERE dgd.device_id=? AND g.visibility='public'`, deviceID).Scan(&pubAll, &pubReadonly)
	if pubAll > 0 {
		return "public", pubReadonly == pubAll
	}
	return "private", false
}

// ListGroupsWithMeta 本节点组列表（含归属节点/可见性）
func (s *Store) ListGroupsWithMeta(tenantID *int64) ([]models.DeviceGroup, error) {
	q := `SELECT id,name,parent_id,node_id,visibility,readonly,created_at FROM device_groups`
	args := []interface{}{}
	if tenantID != nil {
		q += " WHERE tenant_id=?"
		args = append(args, *tenantID)
	}
	q += " ORDER BY id"
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.DeviceGroup{}
	for rows.Next() {
		var g models.DeviceGroup
		var nid, vis sql.NullString
		var ro bool
		var pid sql.NullInt64
		if err := rows.Scan(&g.ID, &g.Name, &pid, &nid, &vis, &ro, &g.CreatedAt); err != nil {
			return nil, err
		}
		if pid.Valid {
			g.ParentID = &pid.Int64
		}
		g.NodeID, g.Visibility, g.Readonly = nid.String, vis.String, ro
		out = append(out, g)
	}
	return out, nil
}

// DeviceGroupsOf 设备所属组
func (s *Store) DeviceGroupsOf(deviceID int64) []models.DeviceGroup {
	rows, err := s.db.Query(`SELECT g.id, g.name, g.node_id, g.visibility FROM device_groups g
		JOIN device_group_devices dgd ON dgd.group_id=g.id WHERE dgd.device_id=?`, deviceID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []models.DeviceGroup{}
	for rows.Next() {
		var g models.DeviceGroup
		rows.Scan(&g.ID, &g.Name, &g.NodeID, &g.Visibility)
		out = append(out, g)
	}
	return out
}
