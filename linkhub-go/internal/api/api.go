package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"linkhub/internal/auth"
	"linkhub/internal/automation"
	"linkhub/internal/cluster"
	"linkhub/internal/connector"
	"linkhub/internal/events"
	"linkhub/internal/models"
	"linkhub/internal/session"
	"linkhub/internal/store"
)

type Handler struct {
	store    *store.Store
	manager  *session.Manager
	authMw   *auth.Middleware
	crypto   *auth.Crypto
	bus      *events.Bus
	cluster  *cluster.Cluster // 可为 nil（未启用集群）
	scheduler *automation.Scheduler
}

func NewHandler(st *store.Store, mgr *session.Manager, mw *auth.Middleware, crypto *auth.Crypto, bus *events.Bus, cl *cluster.Cluster, sched *automation.Scheduler) *Handler {
	return &Handler{store: st, manager: mgr, authMw: mw, crypto: crypto, bus: bus, cluster: cl, scheduler: sched}
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
			r.Get("/api/sessions/{id}/logs/export", h.exportSessionLogs)
			r.Get("/api/serial/ports", h.serialPorts)
			r.Get("/api/connector-kinds", h.connectorKinds)
			r.Get("/api/stats", h.stats)
			r.Get("/api/system/interfaces", h.systemInterfaces)
			r.Get("/api/system/beacon-interfaces", h.getBeaconInterfaces)
		})

		// 操作（operator+）
		r.With(h.authMw.RequireRole(auth.RoleOperator, auth.RoleAdmin)).Group(func(r chi.Router) {
			r.Post("/api/devices", h.createDevice)
			r.Put("/api/devices/{id}", h.updateDevice)
			r.Delete("/api/devices/{id}", h.deleteDevice)
			r.Post("/api/devices/from-template/{key}", h.createFromTemplate)
			r.Post("/api/groups", h.createGroup)
			r.Delete("/api/groups/{id}", h.deleteGroup)
			r.Post("/api/devices/{id}/connections", h.createConnection)
			r.Put("/api/connections/{id}", h.updateConnection)
			r.Delete("/api/connections/{id}", h.deleteConnection)
			r.Post("/api/connections/{id}/open", h.openSession)
			r.Post("/api/sessions/{id}/close", h.closeSession)
			r.Post("/api/serial/test", h.serialTest)
			r.Post("/api/credentials", h.createCredential)
			r.Delete("/api/credentials/{id}", h.deleteCredential)
			// 自动化
			r.Post("/api/playbook/run", h.playbookRun)
			r.Get("/api/tasks", h.listTasks)
			r.Post("/api/tasks", h.createTask)
			r.Put("/api/tasks/{id}", h.updateTask)
			r.Delete("/api/tasks/{id}", h.deleteTask)
			// 发现网络（网卡选择）
			r.Post("/api/system/beacon-interfaces", h.saveBeaconInterfaces)
		})

		// 管理员
		r.With(h.authMw.RequireRole(auth.RoleAdmin)).Group(func(r chi.Router) {
			r.Get("/api/users", h.listUsers)
			r.Post("/api/users", h.createUser)
			r.Put("/api/users/{id}", h.updateUser)
			r.Delete("/api/users/{id}", h.deleteUser)
			r.Post("/api/templates", h.createTemplate)
			r.Delete("/api/templates/{key}", h.deleteTemplate)
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
	nodeStatus := h.nodeStatusMap()
	for i := range devices {
		devices[i].Online = online[devices[i].ID]
		devices[i].NodeOnline = nodeStatus[devices[i].NodeID]
		if devices[i].NodeID == "" || devices[i].NodeID == "local" {
			devices[i].NodeName = "本机"
		}
	}
	// 聚合远程节点目录（集群模式）
	items := devices
	if h.cluster != nil {
		for _, rd := range h.cluster.RemoteDevices() {
			if keyword != "" && !strings.Contains(rd.Name, keyword) && !strings.Contains(rd.Description, keyword) {
				continue
			}
			if tag != "" {
				hit := false
				for _, t := range rd.Tags {
					if t == tag {
						hit = true
						break
					}
				}
				if !hit {
					continue
				}
			}
			if gid != nil && (rd.GroupID == nil || *rd.GroupID != *gid) {
				continue
			}
			items = append(items, rd.Device)
			total++
		}
	}
	jsonOut(w, map[string]interface{}{"total": total, "items": items})
}

// nodeStatusMap 节点在线状态（含本机）
func (h *Handler) nodeStatusMap() map[string]bool {
	out := map[string]bool{"local": true, "": true}
	if h.cluster != nil {
		for _, n := range h.cluster.ListNodes() {
			out[n.NodeID] = n.Status == "online"
			if n.IsSelf {
				out["local"] = true
			}
		}
	}
	return out
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

// selfNodeID 本节点 ID（集群模式为真实节点 ID；对齐 Python 版——本机设备
// 必须带本节点 ID，否则前端 isRemote 误判为远程副本）
func (h *Handler) selfNodeID() string {
	if h.cluster != nil {
		return h.cluster.SelfID()
	}
	return "local"
}

func (h *Handler) createDevice(w http.ResponseWriter, r *http.Request) {
	var d models.Device
	if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
		jsonErr(w, 400, "请求体错误")
		return
	}
	d.NodeID = h.selfNodeID()
	if err := h.store.CreateDevice(&d); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	h.publishDeviceChanged(d.ID)
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
	h.publishDeviceChanged(d.ID)
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
	cascadeDeleteDevice(h.store, h.manager, id)
	h.bus.Publish("device_deleted", map[string]interface{}{"device_id": id})
	auditLog(h, r, "device_delete", d.Name, "")
	w.WriteHeader(204)
}

// publishDeviceChanged 广播设备目录变更（internal/directory 同款结构，供缓存/前端）
func (h *Handler) publishDeviceChanged(deviceID int64) {
	d, err := h.store.GetDevice(deviceID)
	if err != nil || d == nil {
		return
	}
	d.Online = h.manager.OnlineDeviceIDs()[deviceID]
	conns, _ := h.store.ListConnections(deviceID)
	rcs := []map[string]interface{}{}
	for _, c := range conns {
		rcs = append(rcs, map[string]interface{}{
			"id": c.ID, "kind": c.Kind, "name": c.Name, "node_id": c.NodeID, "enabled": c.Enabled,
		})
	}
	h.bus.Publish("device_changed", map[string]interface{}{
		"id": d.ID, "name": d.Name, "description": d.Description, "location": d.Location,
		"owner": d.Owner, "tags": d.Tags, "group_id": d.GroupID, "node_id": h.selfNodeID(),
		"online": d.Online, "connections": rcs,
	})
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

func (h *Handler) deleteGroup(w http.ResponseWriter, r *http.Request) {
	id, _ := paramID(r, "id")
	// 组内设备移出该组
	h.store.Exec(nil, `UPDATE devices SET group_id=NULL WHERE group_id=?`, id)
	if err := h.store.Exec(nil, `DELETE FROM device_groups WHERE id=?`, id); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
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

func (h *Handler) createTemplate(w http.ResponseWriter, r *http.Request) {
	var t models.Template
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		jsonErr(w, 400, "请求体错误")
		return
	}
	if t.Key == "" {
		jsonErr(w, 400, "key 不能为空")
		return
	}
	if existing, _ := h.store.GetTemplateByKey(t.Key); existing != nil {
		jsonErr(w, 409, "模板 key 已存在")
		return
	}
	t.Builtin = false
	if err := h.store.UpsertTemplate(&t); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	auditLog(h, r, "template_create", t.Key, "")
	out, _ := h.store.GetTemplateByKey(t.Key)
	jsonOut(w, out)
}

func (h *Handler) deleteTemplate(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")
	t, _ := h.store.GetTemplateByKey(key)
	if t == nil {
		jsonErr(w, 404, "模板不存在")
		return
	}
	if t.Builtin {
		jsonErr(w, 400, "内置模板不可删除")
		return
	}
	h.store.Exec(nil, `DELETE FROM templates WHERE key=?`, key)
	auditLog(h, r, "template_delete", key, "")
	w.WriteHeader(204)
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
		Description: fmt.Sprintf("基于模板「%s」创建", tpl.Name), NodeID: h.selfNodeID()}
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
	var body models.ConnectionProfile
	json.NewDecoder(r.Body).Decode(&body)
	// 只更新可编辑字段（device_id/kind 不变）
	c.Name = body.Name
	c.Params = body.Params
	c.CredentialID = body.CredentialID
	c.Enabled = body.Enabled
	c.NodeID = body.NodeID
	if err := h.store.UpdateConnection(c); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	h.publishDeviceChanged(c.DeviceID)
	jsonOut(w, c)
}

func (h *Handler) deleteConnection(w http.ResponseWriter, r *http.Request) {
	id, _ := paramID(r, "id")
	c, _ := h.store.GetConnection(id)
	if c == nil {
		jsonErr(w, 404, "连接配置不存在")
		return
	}
	for _, sid := range h.store.SessionIDsByConnection(id) {
		h.manager.Close(sid)
	}
	h.store.DeleteConnection(id)
	h.publishDeviceChanged(c.DeviceID)
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
	// 连接归属远程节点（"端口所在节点"选择）→ 经 internal/open 在对端打开
	if h.cluster != nil && c.NodeID != "" && c.NodeID != "local" && c.NodeID != h.cluster.SelfID() {
		p := h.cluster.GetPeer(c.NodeID)
		if p == nil || p.Status != "online" {
			jsonErr(w, 503, "节点不在线")
			return
		}
		var out struct {
			SessionID int64  `json:"session_id"`
			NodeID    string `json:"node_id"`
		}
		if err := h.cluster.PostJSON(p, "/api/cluster/internal/open",
			map[string]interface{}{"connection_id": c.ID}, &out); err != nil {
			jsonErr(w, 409, err.Error())
			return
		}
		jsonOut(w, out)
		return
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
	if limit > 5000 {
		limit = 5000
	}
	logs, _ := h.store.SessionLogs(id, afterID, limit)
	jsonOut(w, logs)
}

// exportSessionLogs 整段导出为纯文本（密码行打码），text/plain 附件
func (h *Handler) exportSessionLogs(w http.ResponseWriter, r *http.Request) {
	id, _ := paramID(r, "id")
	s, _ := h.store.GetSession(id)
	if s == nil {
		jsonErr(w, 404, "会话不存在")
		return
	}
	logs, err := h.store.AllSessionLogs(id)
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# 灵枢 LinkHub 会话日志 #%d (%s %s)\n", s.ID, s.OpenedBy, s.OpenedAt.Format("2006-01-02 15:04:05"))
	for _, l := range logs {
		raw, err := base64.StdEncoding.DecodeString(l.Data)
		if err != nil {
			continue
		}
		prefix := ""
		switch l.Direction {
		case "tx":
			prefix = "› "
		case "meta":
			prefix = "# "
		}
		b.WriteString(prefix + string(raw))
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=linkhub-session-%d.log", id))
	w.Write([]byte(b.String()))
}

// ---------- 串口 ----------
func (h *Handler) serialPorts(w http.ResponseWriter, r *http.Request) {
	ports := connector.ListPorts()
	out := []map[string]interface{}{}
	for _, p := range ports {
		out = append(out, map[string]interface{}{
			"device": p.Device, "description": p.Description, "is_usb": p.IsUSB,
			"node": "本机", "node_id": "local",
		})
	}
	jsonOut(w, out)
}

func (h *Handler) serialTest(w http.ResponseWriter, r *http.Request) {
	var body map[string]interface{}
	json.NewDecoder(r.Body).Decode(&body)
	c, err := connector.Create("serial", body)
	if err != nil {
		jsonOut(w, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	sc, ok := c.(*connector.SerialConnector)
	if !ok {
		jsonOut(w, map[string]interface{}{"ok": false, "error": "内部错误"})
		return
	}
	if err := sc.OpenWithParams(body); err != nil {
		jsonOut(w, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	defer sc.Close()
	// 探测 banner：读 probe_ms（默认 800ms）窗口内的数据
	probe := 800
	if v, ok := body["probe_ms"].(float64); ok && v >= 100 && v <= 5000 {
		probe = int(v)
	}
	banner := []byte{}
	if rd, err := sc.Read(); err == nil {
		buf := make([]byte, 4096)
		deadline := time.Now().Add(time.Duration(probe) * time.Millisecond)
		for time.Now().Before(deadline) {
			if n, err := rd.Read(buf); n > 0 {
				banner = append(banner, buf[:n]...)
			} else if err != nil {
				break
			} else {
				time.Sleep(30 * time.Millisecond)
			}
		}
	}
	jsonOut(w, map[string]interface{}{"ok": true, "banner": string(banner)})
}

// ---------- 连接器 ----------
func (h *Handler) connectorKinds(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, connectorKindList())
}

// ---------- 统计 ----------
func (h *Handler) stats(w http.ResponseWriter, r *http.Request) {
	devices, total, _ := h.store.ListDevices("", "", nil)
	online := h.manager.OnlineDeviceIDs()
	onlineCount := 0
	for _, d := range devices {
		if online[d.ID] {
			onlineCount++
		}
	}
	nodesTotal, nodesOnline := 0, 0
	if h.cluster != nil {
		nodes := h.cluster.ListNodes()
		nodesTotal = len(nodes)
		for _, n := range nodes {
			if n.Status == "online" {
				nodesOnline++
			}
		}
		// 远程目录计入设备总数
		nodesOnlineOnly := map[string]bool{}
		for _, n := range h.cluster.ListNodes() {
			nodesOnlineOnly[n.NodeID] = n.Status == "online"
		}
		for _, rd := range h.cluster.RemoteDevices() {
			total++
			if rd.Online && nodesOnlineOnly[rd.NodeID] {
				onlineCount++
			}
		}
	}
	templates, _ := h.store.ListTemplates()
	jsonOut(w, map[string]interface{}{
		"devices_total": total, "devices_online": onlineCount,
		"sessions_online": len(h.manager.OnlineSessionIDs()),
		"templates_total": len(templates),
		"nodes_total":     nodesTotal, "nodes_online": nodesOnline,
	})
}

// ---------- 系统 ----------
// systemInterfaces 非 loopback 且 UP 的网卡（IPv4）
func (h *Handler) systemInterfaces(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, systemInterfaceList())
}

// getBeaconInterfaces 已选的发现广播地址（空 = 全部网卡）
func (h *Handler) getBeaconInterfaces(w http.ResponseWriter, r *http.Request) {
	v := h.store.GetMeta("beacon_interfaces")
	out := []string{}
	json.Unmarshal([]byte(v), &out)
	jsonOut(w, map[string]interface{}{"interfaces": out})
}

// saveBeaconInterfaces 保存广播地址选择，beacon 下一轮生效
func (h *Handler) saveBeaconInterfaces(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Broadcasts []string `json:"broadcasts"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	b, _ := json.Marshal(body.Broadcasts)
	h.store.SetMeta("beacon_interfaces", string(b))
	auditLog(h, r, "beacon_ifaces_save", "", strings.Join(body.Broadcasts, ","))
	jsonOut(w, map[string]bool{"ok": true})
}

// systemInterfaceList 供 handler 与 beacon 默认广播共用
func systemInterfaceList() []map[string]interface{} {
	out := []map[string]interface{}{}
	ifaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok || ipnet.IP.To4() == nil || ipnet.IP.IsLoopback() {
				continue
			}
			broadcast := ""
			if len(ipnet.Mask) == 4 {
				bc := make(net.IP, 4)
				for i := range bc {
					bc[i] = ipnet.IP[i] | ^ipnet.Mask[i]
				}
				broadcast = bc.String()
			}
			out = append(out, map[string]interface{}{
				"name": ifc.Name, "ip": ipnet.IP.String(),
				"netmask": net.IP(ipnet.Mask).String(), "broadcast": broadcast,
				"is_up": true,
			})
		}
	}
	return out
}

// DefaultBeaconBroadcasts 未配置时：全部网卡的广播地址 + 受限广播（main.go beacon 用）
func DefaultBeaconBroadcasts() []string {
	out := []string{"255.255.255.255"}
	for _, i := range systemInterfaceList() {
		if b, _ := i["broadcast"].(string); b != "" && b != "0.0.0.0" {
			out = append(out, b)
		}
	}
	return out
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

func (h *Handler) updateUser(w http.ResponseWriter, r *http.Request) {
	id, _ := paramID(r, "id")
	u, err := h.store.GetUser(id)
	if err != nil || u == nil {
		jsonErr(w, 404, "用户不存在")
		return
	}
	var body struct {
		Name     string  `json:"name"`
		Password *string `json:"password"`
		Role     *string `json:"role"`
		Enabled  *bool   `json:"enabled"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.Name != "" {
		u.Name = body.Name
	}
	if body.Role != nil {
		switch *body.Role {
		case "admin", "operator", "viewer":
			u.Role = *body.Role
		default:
			jsonErr(w, 400, "角色必须是 admin/operator/viewer")
			return
		}
	}
	if body.Enabled != nil {
		u.Enabled = *body.Enabled
	}
	if body.Password != nil && *body.Password != "" {
		u.PasswordHash = auth.HashPassword(*body.Password, "")
	}
	if err := h.store.UpdateUser(u); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	auditLog(h, r, "user_update", u.Name, "")
	jsonOut(w, u)
}

func (h *Handler) deleteUser(w http.ResponseWriter, r *http.Request) {
	id, _ := paramID(r, "id")
	u := auth.UserFrom(r.Context())
	if u.ID == id {
		jsonErr(w, 400, "不能删除自己")
		return
	}
	if err := h.store.DeleteUser(id); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	auditLog(h, r, "user_delete", fmt.Sprintf("#%d", id), "")
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
	if err := h.store.DeleteCredential(id); err != nil {
		jsonErr(w, 409, "删除失败：可能仍被连接配置引用")
		return
	}
	auditLog(h, r, "credential_delete", fmt.Sprintf("#%d", id), "")
	w.WriteHeader(204)
}

// ---------- 自动化（playbook / 定时任务） ----------
func (h *Handler) playbookRun(w http.ResponseWriter, r *http.Request) {
	var pb automation.Playbook
	if err := json.NewDecoder(r.Body).Decode(&pb); err != nil {
		jsonErr(w, 400, "请求体错误")
		return
	}
	runner := automation.NewRunner(h.store, h.manager, h.cluster, h.bus)
	auditLog(h, r, "playbook_run", pb.Name, "")
	jsonOut(w, runner.Run(&pb))
}

func (h *Handler) listTasks(w http.ResponseWriter, r *http.Request) {
	tasks, _ := h.store.ListTasks()
	jsonOut(w, tasks)
}

func (h *Handler) createTask(w http.ResponseWriter, r *http.Request) {
	var t models.ScheduledTask
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		jsonErr(w, 400, "请求体错误")
		return
	}
	if t.Name == "" || t.Command == "" {
		jsonErr(w, 400, "name 与 command 必填")
		return
	}
	if t.IntervalS < 30 {
		t.IntervalS = 30
	}
	if t.WaitMs <= 0 {
		t.WaitMs = 1500
	}
	if err := h.store.CreateTask(&t); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	if h.scheduler != nil && t.Enabled {
		h.scheduler.Track(t.ID)
	}
	auditLog(h, r, "task_create", t.Name, "")
	w.WriteHeader(201)
	jsonOut(w, t)
}

func (h *Handler) updateTask(w http.ResponseWriter, r *http.Request) {
	id, _ := paramID(r, "id")
	t, err := h.store.GetTask(id)
	if err != nil || t == nil {
		jsonErr(w, 404, "任务不存在")
		return
	}
	var body map[string]interface{}
	json.NewDecoder(r.Body).Decode(&body)
	if v, ok := body["name"].(string); ok {
		t.Name = v
	}
	if v, ok := body["command"].(string); ok {
		t.Command = v
	}
	if v, ok := body["interval_s"].(float64); ok {
		t.IntervalS = int(v)
		if t.IntervalS < 30 {
			t.IntervalS = 30
		}
	}
	if v, ok := body["wait_ms"].(float64); ok {
		t.WaitMs = int(v)
	}
	if v, ok := body["enabled"].(bool); ok {
		t.Enabled = v
	}
	if v, ok := body["targets"].([]interface{}); ok {
		b, _ := json.Marshal(v)
		targets := []map[string]interface{}{}
		json.Unmarshal(b, &targets)
		t.Targets = targets
	}
	if err := h.store.UpdateTask(t); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	if h.scheduler != nil {
		h.scheduler.Untrack(t.ID)
		if t.Enabled {
			h.scheduler.Track(t.ID)
		}
	}
	auditLog(h, r, "task_update", t.Name, "")
	jsonOut(w, t)
}

func (h *Handler) deleteTask(w http.ResponseWriter, r *http.Request) {
	id, _ := paramID(r, "id")
	if h.scheduler != nil {
		h.scheduler.Untrack(id)
	}
	if err := h.store.DeleteTask(id); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	auditLog(h, r, "task_delete", fmt.Sprintf("#%d", id), "")
	w.WriteHeader(204)
}
