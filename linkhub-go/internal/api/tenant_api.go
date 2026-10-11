package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"linkhub/internal/auth"
	"linkhub/internal/models"
)

// RegisterTenantRoutes 租户管理（平台管理员）+ 成员管理（平台/租户管理员）
func (h *Handler) RegisterTenantRoutes(r chi.Router) {
	// 登录相关（公开）
	r.Post("/api/auth/tenants", h.loginTenants) // 登录页查账号可用租户（需密码验证）
	r.Post("/api/auth/switch-tenant", h.switchTenant)

	// 租户管理（平台管理员；成员管理放宽到本租户 tenant_admin）
	idFrom := func(r *http.Request) int64 {
		id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		return id
	}
	r.Group(func(ar chi.Router) {
		ar.Use(h.authMw.User)
		ar.Route("/api/tenants", func(r chi.Router) {
			r.With(func(next http.Handler) http.Handler { return h.authMw.RequireTenantAdminOf(idFrom, next) }).Get("/", h.listTenants)
			r.With(h.authMw.RequirePlatformAdminHTTP).Post("/", h.createTenant)
			r.Route("/{id}", func(r chi.Router) {
				guard := func(next http.HandlerFunc) http.HandlerFunc {
					return func(w http.ResponseWriter, r *http.Request) {
						h.authMw.RequireTenantAdminOf(idFrom, http.HandlerFunc(next)).ServeHTTP(w, r)
					}
				}
				r.Get("/", guard(h.getTenant))
				r.Put("/", guard(h.updateTenant))
				r.With(h.authMw.RequirePlatformAdminHTTP).Delete("/", h.deleteTenant)
				r.Get("/members", guard(h.listMembers))
				r.Post("/members", guard(h.addMember))
				r.Put("/members/{user_id}", guard(h.updateMember))
				r.Delete("/members/{user_id}", guard(h.removeMember))
				r.With(h.authMw.RequirePlatformAdminHTTP).Get("/nodes", h.tenantNodes)
				r.With(h.authMw.RequirePlatformAdminHTTP).Put("/nodes", h.setTenantNodes)
			})
		})
	})
}

// loginTenants 登录第一步：账号+密码 → 可用租户列表（多租户选择）
func (h *Handler) loginTenants(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	u, _ := h.store.GetUserByName(body.Name)
	if u == nil || !auth.VerifyPassword(body.Password, u.PasswordHash) || !u.Enabled {
		jsonErr(w, 401, "用户名或密码错误")
		return
	}
	tenants, _ := h.store.TenantsOfUser(u.ID)
	list := []map[string]interface{}{}
	for _, m := range tenants {
		list = append(list, map[string]interface{}{
			"tenant_id": m.TenantID, "name": m.TenantName, "code": m.TenantCode, "role": m.Role,
		})
	}
	if h.store.IsPlatformAdmin(u.ID) {
		all, _ := h.store.ListTenants()
		list = append(list, map[string]interface{}{
			"tenant_id": 0, "name": "平台管理（全部租户）", "code": "__platform__", "role": "platform_admin",
		})
		_ = all
	}
	if len(list) == 0 {
		jsonErr(w, 403, "该账号未加入任何租户，请联系管理员")
		return
	}
	jsonOut(w, map[string]interface{}{"tenants": list})
}

// TenantLogin 两步登录的第二步（原 login 增强）：name/password/tenant_code
func (h *Handler) tenantLogin(w http.ResponseWriter, r *http.Request, u *models.User, tenantCode string) {
	pa := h.store.IsPlatformAdmin(u.ID)
	var tenant *models.Tenant
	var role string
	if tenantCode == "__platform__" {
		if !pa {
			jsonErr(w, 403, "非平台管理员不能使用平台视图")
			return
		}
		role = "platform_admin"
	} else {
		tenant, _ = h.store.GetTenantByCode(tenantCode)
		if tenant == nil || tenant.Status != "active" {
			jsonErr(w, 401, "租户不存在或已停用")
			return
		}
		mem, _ := h.store.GetMembership(tenant.ID, u.ID)
		if mem == nil && !pa {
			jsonErr(w, 403, "该账号不属于此租户")
			return
		}
		if mem != nil {
			role = mem.Role
		} else {
			role = "tenant_admin" // 平台管理员进任意租户当管理员
		}
	}
	tid := int64(0)
	tenantName := "平台管理"
	if tenant != nil {
		tid = tenant.ID
		tenantName = tenant.Name
	}
	token := auth.MakeTenantToken(u, h.store.GetMeta("secret_key"), tid, role, pa)
	http.SetCookie(w, &http.Cookie{Name: "linkhub_token", Value: token, Path: "/", HttpOnly: true, MaxAge: 7 * 24 * 3600})
	jsonOut(w, map[string]interface{}{
		"token": token,
		"user":  map[string]interface{}{"id": u.ID, "name": u.Name, "role": role, "enabled": u.Enabled},
		"tenant": map[string]interface{}{
			"id": tid, "code": tenantCode, "name": tenantName,
		},
	})
}

// switchTenant 平台管理员切换租户（重签 token）
func (h *Handler) switchTenant(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TenantCode string `json:"tenant_code"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	// 公开路由：自行鉴权（带旧 token）
	ctx, reject := h.authMw.Authenticate(r)
	if reject != "" {
		jsonErr(w, 401, reject)
		return
	}
	u := auth.UserFrom(ctx)
	if u == nil {
		jsonErr(w, 401, "未登录")
		return
	}
	h.tenantLogin(w, r, u, body.TenantCode)
}

// ---------- 租户 CRUD（平台管理员） ----------
func (h *Handler) listTenants(w http.ResponseWriter, r *http.Request) {
	list, err := h.store.ListTenants()
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	out := []map[string]interface{}{}
	for _, t := range list {
		members, _ := h.store.MembersOfTenant(t.ID)
		out = append(out, map[string]interface{}{
			"id": t.ID, "name": t.Name, "code": t.Code, "status": t.Status, "remark": t.Remark,
			"created_at": t.CreatedAt, "members": len(members),
		})
	}
	jsonOut(w, out)
}

func (h *Handler) createTenant(w http.ResponseWriter, r *http.Request) {
	var t models.Tenant
	json.NewDecoder(r.Body).Decode(&t)
	if t.Name == "" || t.Code == "" {
		jsonErr(w, 400, "name 与 code 必填")
		return
	}
	if exist, _ := h.store.GetTenantByCode(t.Code); exist != nil {
		jsonErr(w, 409, "租户 code 已存在")
		return
	}
	if err := h.store.CreateTenant(&t); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	auditLog(h, r, "tenant_create", t.Name, t.Code)
	jsonOut(w, t)
}

func (h *Handler) getTenant(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	t, _ := h.store.GetTenant(id)
	if t == nil {
		jsonErr(w, 404, "租户不存在")
		return
	}
	jsonOut(w, t)
}

func (h *Handler) updateTenant(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	t, _ := h.store.GetTenant(id)
	if t == nil {
		jsonErr(w, 404, "租户不存在")
		return
	}
	var body models.Tenant
	json.NewDecoder(r.Body).Decode(&body)
	if body.Name != "" {
		t.Name = body.Name
	}
	if body.Status != "" {
		t.Status = body.Status
	}
	t.Remark = body.Remark
	if err := h.store.UpdateTenant(t); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	auditLog(h, r, "tenant_update", t.Name, "")
	jsonOut(w, t)
}

func (h *Handler) deleteTenant(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if id == 1 {
		jsonErr(w, 400, "默认租户不可删除")
		return
	}
	if err := h.store.DeleteTenant(id); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	auditLog(h, r, "tenant_delete", "#"+strconv.FormatInt(id, 10), "")
	w.WriteHeader(204)
}

// ---------- 成员管理 ----------
func (h *Handler) listMembers(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	members, err := h.store.MembersOfTenant(id)
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	jsonOut(w, members)
}

func (h *Handler) addMember(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var body struct {
		Name string `json:"name"`
		Role string `json:"role"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.Role == "" {
		body.Role = "viewer"
	}
	u, _ := h.store.GetUserByName(body.Name)
	if u == nil {
		jsonErr(w, 404, "用户不存在（先在系统管理创建账号）")
		return
	}
	if body.Role != "tenant_admin" && body.Role != "operator" && body.Role != "viewer" {
		jsonErr(w, 400, "角色必须是 tenant_admin/operator/viewer")
		return
	}
	if err := h.store.AddMember(id, u.ID, body.Role); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	auditLog(h, r, "tenant_member_add", body.Name, body.Role)
	jsonOut(w, map[string]bool{"ok": true})
}

func (h *Handler) updateMember(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	uid, _ := strconv.ParseInt(chi.URLParam(r, "user_id"), 10, 64)
	var body struct {
		Role string `json:"role"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if err := h.store.UpdateMemberRole(id, uid, body.Role); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	auditLog(h, r, "tenant_member_update", "", body.Role)
	jsonOut(w, map[string]bool{"ok": true})
}

func (h *Handler) removeMember(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	uid, _ := strconv.ParseInt(chi.URLParam(r, "user_id"), 10, 64)
	if err := h.store.RemoveMember(id, uid); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	auditLog(h, r, "tenant_member_remove", "", "")
	w.WriteHeader(204)
}

// ---------- 节点白名单 ----------
func (h *Handler) tenantNodes(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	jsonOut(w, map[string]interface{}{"nodes": h.store.TenantNodes(id)})
}

func (h *Handler) setTenantNodes(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var body struct {
		Nodes []string `json:"nodes"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if err := h.store.SetTenantNodes(id, body.Nodes); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	auditLog(h, r, "tenant_nodes_set", "", strconv.Itoa(len(body.Nodes))+" 个节点")
	jsonOut(w, map[string]bool{"ok": true})
}

// RegisterDeviceGroupRoutes 设备组管理（组=成员+设备；帐号管理页入口）
func (h *Handler) RegisterDeviceGroupRoutes(r chi.Router) {
	r.Group(func(ar chi.Router) {
		ar.Use(h.authMw.User)
		ar.Route("/api/device-groups", func(r chi.Router) {
			r.With(h.authMw.RequireTenantAdminHTTP).Get("/", h.listDeviceGroups)
			r.With(h.authMw.RequireTenantAdminHTTP).Post("/", h.createDeviceGroup)
			r.Route("/{id}", func(r chi.Router) {
				r.With(h.authMw.RequireTenantAdminHTTP).Get("/", h.getDeviceGroup)
				r.With(h.authMw.RequireTenantAdminHTTP).Delete("/", h.deleteDeviceGroup)
				r.With(h.authMw.RequireTenantAdminHTTP).Post("/members/{user_id}", h.addGroupMember)
				r.With(h.authMw.RequireTenantAdminHTTP).Delete("/members/{user_id}", h.removeGroupMember)
				r.With(h.authMw.RequireTenantAdminHTTP).Put("/devices", h.setGroupDevices)
			})
			// 用户视角：自己的组与可见设备（所有登录用户）
			r.Get("/mine", h.myGroups)
		})
	})
}

func (h *Handler) listDeviceGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := h.store.ListGroups(h.tenantFilter(r))
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	out := []map[string]interface{}{}
	for _, g := range groups {
		memberIDs, _ := h.store.GroupMembers(g.ID)
		deviceIDs := h.store.GroupDevices(g.ID)
		out = append(out, map[string]interface{}{
			"id": g.ID, "name": g.Name, "parent_id": g.ParentID,
			"members": len(memberIDs), "devices": len(deviceIDs),
		})
	}
	jsonOut(w, out)
}

func (h *Handler) createDeviceGroup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.Name == "" {
		jsonErr(w, 400, "组名必填")
		return
	}
	g, err := h.store.CreateGroup(body.Name, nil)
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	auditLog(h, r, "device_group_create", g.Name, "")
	jsonOut(w, g)
}

func (h *Handler) getDeviceGroup(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	groups, _ := h.store.ListGroups(nil)
	var found *models.DeviceGroup
	for i, g := range groups {
		if g.ID == id {
			found = &groups[i]
			break
		}
	}
	if found == nil {
		jsonErr(w, 404, "组不存在")
		return
	}
	memberIDs, _ := h.store.GroupMembers(id)
	memberNames := []map[string]interface{}{}
	for _, uid := range memberIDs {
		if u, _ := h.store.GetUser(uid); u != nil {
			memberNames = append(memberNames, map[string]interface{}{"id": u.ID, "name": u.Name})
		}
	}
	jsonOut(w, map[string]interface{}{
		"id": found.ID, "name": found.Name,
		"members": memberNames, "device_ids": h.store.GroupDevices(id),
	})
}

func (h *Handler) deleteDeviceGroup(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	h.store.Exec(nil, `DELETE FROM device_group_devices WHERE group_id=?`, id)
	h.store.Exec(nil, `DELETE FROM device_group_members WHERE group_id=?`, id)
	h.store.Exec(nil, `DELETE FROM device_groups WHERE id=?`, id)
	auditLog(h, r, "device_group_delete", "#"+strconv.FormatInt(id, 10), "")
	w.WriteHeader(204)
}

func (h *Handler) addGroupMember(w http.ResponseWriter, r *http.Request) {
	gid, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	uid, _ := strconv.ParseInt(chi.URLParam(r, "user_id"), 10, 64)
	if err := h.store.AddGroupMember(gid, uid); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	auditLog(h, r, "device_group_member_add", "", "")
	jsonOut(w, map[string]bool{"ok": true})
}

func (h *Handler) removeGroupMember(w http.ResponseWriter, r *http.Request) {
	gid, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	uid, _ := strconv.ParseInt(chi.URLParam(r, "user_id"), 10, 64)
	if err := h.store.RemoveGroupMember(gid, uid); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}

func (h *Handler) setGroupDevices(w http.ResponseWriter, r *http.Request) {
	gid, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var body struct {
		DeviceIDs []int64 `json:"device_ids"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if err := h.store.SetGroupDevices(gid, body.DeviceIDs); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	auditLog(h, r, "device_group_devices_set", "", strconv.Itoa(len(body.DeviceIDs))+" 台")
	jsonOut(w, map[string]bool{"ok": true})
}

// myGroups 当前用户的组（帐号管理页/设备页筛选用）
func (h *Handler) myGroups(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		jsonErr(w, 401, "未登录")
		return
	}
	groups, err := h.store.GroupsOfUser(u.ID)
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	jsonOut(w, groups)
}
