package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"linkhub/internal/auth"
	"linkhub/internal/connector"
	"linkhub/internal/models"
	"linkhub/internal/session"
	"linkhub/internal/store"
)

type Handler struct {
	store    *store.Store
	manager  *session.Manager
	authMw   *auth.Middleware
	crypto   *auth.Crypto
}

func NewHandler(st *store.Store, mgr *session.Manager, mw *auth.Middleware, crypto *auth.Crypto) *Handler {
	return &Handler{store: st, manager: mgr, authMw: mw, crypto: crypto}
}

func (h *Handler) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// 公开
	r.Post("/api/auth/login", h.login)
	r.Post("/api/auth/logout", h.logout)
	r.Get("/api/health", h.health)

	// 需登录
	r.Group(func(r chi.Router) {
		r.Use(h.authMw.User)
		r.Get("/api/auth/me", h.me)

		// 只读（viewer+）
		r.With(h.authMw.RequireRole(auth.RoleViewer, auth.RoleOperator, auth.RoleAdmin)).Group(func(r chi.Router) {
			r.Get("/api/devices", h.listDevices)
			r.Get("/api/devices/{id}", h.getDevice)
			r.Get("/api/groups", h.listGroups)
			r.Get("/api/templates", h.listTemplates)
			r.Get("/api/templates/{key}", h.getTemplate)
			r.Get("/api/devices/{id}/connections", h.listConnections)
			r.Get("/api/sessions", h.listSessions)
			r.Get("/api/sessions/{id}", h.getSession)
			r.Get("/api/sessions/{id}/logs", h.sessionLogs)
			r.Get("/api/serial/ports", h.serialPorts)
			r.Get("/api/connector-kinds", h.connectorKinds)
			r.Get("/api/stats", h.stats)
		})

		// 操作（operator+）
		r.With(h.authMw.RequireRole(auth.RoleOperator, auth.RoleAdmin)).Group(func(r chi.Router) {
			r.Post("/api/devices", h.createDevice)
			r.Put("/api/devices/{id}", h.updateDevice)
			r.Delete("/api/devices/{id}", h.deleteDevice)
			r.Post("/api/devices/from-template/{key}", h.createFromTemplate)
			r.Post("/api/groups", h.createGroup)
			r.Post("/api/devices/{id}/connections", h.createConnection)
			r.Put("/api/connections/{id}", h.updateConnection)
			r.Delete("/api/connections/{id}", h.deleteConnection)
			r.Post("/api/connections/{id}/open", h.openSession)
			r.Post("/api/sessions/{id}/close", h.closeSession)
			r.Post("/api/serial/test", h.serialTest)
			r.Post("/api/credentials", h.createCredential)
			r.Delete("/api/credentials/{id}", h.deleteCredential)
		})

		// 管理员
		r.With(h.authMw.RequireRole(auth.RoleAdmin)).Group(func(r chi.Router) {
			r.Get("/api/users", h.listUsers)
			r.Post("/api/users", h.createUser)
			r.Delete("/api/users/{id}", h.deleteUser)
			r.Get("/api/audit", h.listAudit)
		})
	})
	return r
}

// ---------- helpers ----------
func jsonOut(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"detail": msg})
}

func paramID(r *http.Request, name string) (int64, error) {
	return strconv.ParseInt(chi.URLParam(r, name), 10, 64)
}

func auditLog(h *Handler, r *http.Request, action, target, detail string) {
	u := auth.UserFrom(r.Context())
	name := "?"
	if u != nil {
		name = u.Name
	}
	h.store.Audit(name, action, target, detail)
}

// ---------- 认证 ----------
func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	u, _ := h.store.GetUserByName(body.Name)
	if u == nil || !auth.VerifyPassword(body.Password, u.PasswordHash) {
		jsonErr(w, 401, "用户名或密码错误")
		return
	}
	if !u.Enabled {
		jsonErr(w, 403, "用户已禁用")
		return
	}
	token := auth.MakeToken(u, h.store.GetMeta("secret_key"))
	http.SetCookie(w, &http.Cookie{Name: "linkhub_token", Value: token, Path: "/", HttpOnly: true, MaxAge: 7 * 24 * 3600})
	auditLog(h, r, "login", "", "")
	jsonOut(w, map[string]interface{}{
		"token": token,
		"user":  map[string]interface{}{"id": u.ID, "name": u.Name, "role": u.Role, "enabled": u.Enabled},
	})
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "linkhub_token", Value: "", Path: "/", MaxAge: -1})
	jsonOut(w, map[string]bool{"ok": true})
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	jsonOut(w, map[string]interface{}{"id": u.ID, "name": u.Name, "role": u.Role, "enabled": u.Enabled})
}

func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, map[string]string{"status": "ok", "app": "灵枢 LinkHub", "mascot": "🐙连连"})
}

// ---------- 设备 ----------
func (h *Handler) listDevices(w http.ResponseWriter, r *http.Request) {
	keyword := r.URL.Query().Get("keyword")
	tag := r.URL.Query().Get("tag")
	var gid *int64
	if g := r.URL.Query().Get("group_id"); g != "" {
		if v, err := strconv.ParseInt(g, 10, 64); err == nil {
			gid = &v
		}
	}
	devices, total, err := h.store.ListDevices(keyword, tag, gid)
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	online := h.manager.OnlineDeviceIDs()
	for i := range devices {
		devices[i].Online = online[devices[i].ID]
		devices[i].NodeName = devices[i].NodeID
		devices[i].NodeOnline = true
	}
	jsonOut(w, map[string]interface{}{"total": total, "items": devices})
}

func (h *Handler) getDevice(w http.ResponseWriter, r *http.Request) {
	id, _ := paramID(r, "id")
	d, err := h.store.GetDevice(id)
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	if d == nil {
		jsonErr(w, 404, "设备不存在")
		return
	}
	d.Online = h.manager.OnlineDeviceIDs()[d.ID]
	jsonOut(w, d)
}

func (h *Handler) createDevice(w http.ResponseWriter, r *http.Request) {
	var d models.Device
	if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
		jsonErr(w, 400, "请求体错误")
		return
	}
	d.NodeID = "local"
	if err := h.store.CreateDevice(&d); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	auditLog(h, r, "device_create", d.Name, "")
	jsonOut(w, d)
}

func (h *Handler) updateDevice(w http.ResponseWriter, r *http.Request) {
	id, _ := paramID(r, "id")
	d, err := h.store.GetDevice(id)
	if err != nil || d == nil {
		jsonErr(w, 404, "设备不存在")
		return
	}
	var body models.Device
	json.NewDecoder(r.Body).Decode(&body)
	d.Name, d.Description, d.Location, d.Owner, d.Tags, d.GroupID = body.Name, body.Description, body.Location, body.Owner, body.Tags, body.GroupID
	if err := h.store.UpdateDevice(d); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	auditLog(h, r, "device_update", d.Name, "")
	jsonOut(w, d)
}

func (h *Handler) deleteDevice(w http.ResponseWriter, r *http.Request) {
	id, _ := paramID(r, "id")
	d, _ := h.store.GetDevice(id)
	if d == nil {
		jsonErr(w, 404, "设备不存在")
		return
	}
	// 关闭其会话
	conns, _ := h.store.ListConnections(id)
	for _, c := range conns {
		_ = c
	}
	// TODO: close sessions by connection
	h.store.DeleteDevice(id)
	auditLog(h, r, "device_delete", d.Name, "")
	w.WriteHeader(204)
}

// ---------- 分组 ----------
func (h *Handler) listGroups(w http.ResponseWriter, r *http.Request) {
	g, _ := h.store.ListGroups()
	jsonOut(w, g)
}

func (h *Handler) createGroup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string `json:"name"`
		ParentID *int64 `json:"parent_id"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	g, err := h.store.CreateGroup(body.Name, body.ParentID)
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	jsonOut(w, g)
}

// ---------- 模板 ----------
func (h *Handler) listTemplates(w http.ResponseWriter, r *http.Request) {
	t, _ := h.store.ListTemplates()
	jsonOut(w, t)
}

func (h *Handler) getTemplate(w http.ResponseWriter, r *http.Request) {
	t, _ := h.store.GetTemplateByKey(chi.URLParam(r, "key"))
	if t == nil {
		jsonErr(w, 404, "模板不存在")
		return
	}
	jsonOut(w, t)
}

func (h *Handler) createFromTemplate(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")
	var body struct {
		Name           string                 `json:"name"`
		GroupID        *int64                 `json:"group_id"`
		ParamOverrides map[string]interface{} `json:"param_overrides"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	tpl, _ := h.store.GetTemplateByKey(key)
	if tpl == nil {
		jsonErr(w, 404, "模板不存在")
		return
	}
	d := models.Device{Name: body.Name, GroupID: body.GroupID, TemplateID: &tpl.ID,
		Description: fmt.Sprintf("基于模板「%s」创建", tpl.Name), NodeID: "local"}
	h.store.CreateDevice(&d)
	// 建默认连接配置
	for _, conn := range tpl.DefaultConnections {
		kind, _ := conn["kind"].(string)
		name, _ := conn["name"].(string)
		params, _ := conn["params"].(map[string]interface{})
		h.store.CreateConnection(&models.ConnectionProfile{
			DeviceID: d.ID, Kind: kind, Name: name, Params: params, Enabled: true, NodeID: "local",
		})
	}
	auditLog(h, r, "device_from_template", d.Name, key)
	jsonOut(w, d)
}

// ---------- 连接配置 ----------
func (h *Handler) listConnections(w http.ResponseWriter, r *http.Request) {
	id, _ := paramID(r, "id")
	c, _ := h.store.ListConnections(id)
	jsonOut(w, c)
}

func (h *Handler) createConnection(w http.ResponseWriter, r *http.Request) {
	id, _ := paramID(r, "id")
	var c models.ConnectionProfile
	json.NewDecoder(r.Body).Decode(&c)
	c.DeviceID = id
	if c.NodeID == "" {
		c.NodeID = "local"
	}
	if err := h.store.CreateConnection(&c); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	jsonOut(w, c)
}

func (h *Handler) updateConnection(w http.ResponseWriter, r *http.Request) {
	id, _ := paramID(r, "id")
	c, _ := h.store.GetConnection(id)
	if c == nil {
		jsonErr(w, 404, "连接配置不存在")
		return
	}
	json.NewDecoder(r.Body).Decode(c)
	// TODO: store update
	jsonOut(w, c)
}

func (h *Handler) deleteConnection(w http.ResponseWriter, r *http.Request) {
	id, _ := paramID(r, "id")
	h.store.DeleteConnection(id)
	w.WriteHeader(204)
}

// ---------- 会话 ----------
func (h *Handler) openSession(w http.ResponseWriter, r *http.Request) {
	id, _ := paramID(r, "id")
	c, _ := h.store.GetConnection(id)
	if c == nil {
		jsonErr(w, 404, "连接配置不存在")
		return
	}
	if !c.Enabled {
		jsonErr(w, 409, "连接配置已禁用")
		return
	}
	u := auth.UserFrom(r.Context())
	openedBy := "admin"
	if u != nil {
		openedBy = u.Name
	}
	s, err := h.manager.Open(id, c.Kind, c.Params, c.DeviceID, openedBy)
	if err != nil {
		jsonErr(w, 409, err.Error())
		return
	}
	jsonOut(w, map[string]interface{}{"session_id": s.ID, "node_id": "local"})
}

func (h *Handler) listSessions(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	sessions, _ := h.store.ListSessions(status)
	// 运行态覆盖
	for i := range sessions {
		if rt := h.manager.Get(sessions[i].ID); rt != nil {
			sessions[i].Status = rt.Status
			sessions[i].LastError = rt.LastError
		}
	}
	jsonOut(w, sessions)
}

func (h *Handler) getSession(w http.ResponseWriter, r *http.Request) {
	id, _ := paramID(r, "id")
	s, _ := h.store.GetSession(id)
	if s == nil {
		jsonErr(w, 404, "会话不存在")
		return
	}
	if rt := h.manager.Get(id); rt != nil {
		s.Status, s.LastError = rt.Status, rt.LastError
	}
	jsonOut(w, s)
}

func (h *Handler) closeSession(w http.ResponseWriter, r *http.Request) {
	id, _ := paramID(r, "id")
	h.manager.Close(id)
	jsonOut(w, map[string]bool{"closed": true})
}

func (h *Handler) sessionLogs(w http.ResponseWriter, r *http.Request) {
	id, _ := paramID(r, "id")
	afterID, _ := strconv.ParseInt(r.URL.Query().Get("after_id"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 500
	}
	logs, _ := h.store.SessionLogs(id, afterID, limit)
	jsonOut(w, logs)
}

// ---------- 串口 ----------
func (h *Handler) serialPorts(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, connector.ListPorts())
}

func (h *Handler) serialTest(w http.ResponseWriter, r *http.Request) {
	var body map[string]interface{}
	json.NewDecoder(r.Body).Decode(&body)
	c, err := connector.Create("serial", body)
	if err != nil {
		jsonOut(w, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	if sc, ok := c.(*connector.SerialConnector); ok {
		if err := sc.OpenWithParams(body); err != nil {
			jsonOut(w, map[string]interface{}{"ok": false, "error": err.Error()})
			return
		}
		defer sc.Close()
		jsonOut(w, map[string]interface{}{"ok": true, "banner": ""})
	}
}

// ---------- 连接器 ----------
func (h *Handler) connectorKinds(w http.ResponseWriter, r *http.Request) {
	kinds := []map[string]interface{}{}
	for _, k := range connector.Kinds() {
		kinds = append(kinds, map[string]interface{}{"kind": k, "schema": map[string]interface{}{}})
	}
	jsonOut(w, kinds)
}

// ---------- 统计 ----------
func (h *Handler) stats(w http.ResponseWriter, r *http.Request) {
	devices, total, _ := h.store.ListDevices("", "", nil)
	online := h.manager.OnlineDeviceIDs()
	onlineCount := 0
	for range devices {
		_ = devices
	}
	for id := range online {
		if id > 0 {
			onlineCount++
		}
	}
	templates, _ := h.store.ListTemplates()
	jsonOut(w, map[string]interface{}{
		"devices_total": total, "devices_online": onlineCount,
		"sessions_online": len(h.manager.OnlineSessionIDs()),
		"templates_total": len(templates),
		"nodes_total": 0, "nodes_online": 0,
	})
}

// ---------- 用户/审计 ----------
func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request) {
	u, _ := h.store.ListUsers()
	jsonOut(w, u)
}

func (h *Handler) createUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string `json:"name"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.Role == "" {
		body.Role = "viewer"
	}
	if body.Password == "" {
		jsonErr(w, 400, "必须设置初始密码")
		return
	}
	u := models.User{Name: body.Name, Role: body.Role, Enabled: true}
	u.PasswordHash = auth.HashPassword(body.Password, "")
	if err := h.store.CreateUser(&u); err != nil {
		jsonErr(w, 409, "用户已存在")
		return
	}
	auditLog(h, r, "user_create", body.Name, "")
	jsonOut(w, u)
}

func (h *Handler) deleteUser(w http.ResponseWriter, r *http.Request) {
	id, _ := paramID(r, "id")
	u := auth.UserFrom(r.Context())
	if u.ID == id {
		jsonErr(w, 400, "不能删除自己")
		return
	}
	// TODO: store delete user
	w.WriteHeader(204)
}

func (h *Handler) listAudit(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 100
	}
	a, _ := h.store.ListAudit(limit)
	jsonOut(w, a)
}

// ---------- 凭证 ----------
func (h *Handler) createCredential(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name   string `json:"name"`
		Type   string `json:"type"`
		Secret string `json:"secret"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	enc, err := h.crypto.Encrypt(body.Secret)
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	c, err := h.store.CreateCredential(body.Name, body.Type, enc)
	if err != nil {
		jsonErr(w, 409, "凭证名已存在")
		return
	}
	auditLog(h, r, "credential_create", body.Name, "")
	jsonOut(w, c)
}

func (h *Handler) deleteCredential(w http.ResponseWriter, r *http.Request) {
	id, _ := paramID(r, "id")
	// TODO: store delete credential
	_ = id
	w.WriteHeader(204)
}
