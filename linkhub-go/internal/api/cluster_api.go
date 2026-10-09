package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"linkhub/internal/cluster"
	"linkhub/internal/connector"
	"linkhub/internal/models"
	"linkhub/internal/session"
	"linkhub/internal/store"
)

// ClusterHandler 集群相关接口（对外 proxy + 对内 internal）
type ClusterHandler struct {
	cluster *cluster.Cluster // nil 表示集群模式未启用
	store   *store.Store
	manager *session.Manager
	enabled bool
	name    string
	address string
}

func NewClusterHandler(c *cluster.Cluster, st *store.Store, mgr *session.Manager, enabled bool, name, address string) *ClusterHandler {
	return &ClusterHandler{cluster: c, store: st, manager: mgr, enabled: enabled, name: name, address: address}
}

func (h *ClusterHandler) RegisterRoutes(r interface {
	Post(string, http.HandlerFunc)
	Get(string, http.HandlerFunc)
	Put(string, http.HandlerFunc)
	Delete(string, http.HandlerFunc)
}) {
	// 前端用
	r.Get("/api/cluster/info", h.info)
	r.Get("/api/cluster/nodes", h.nodes)
	r.Get("/api/cluster/discovered", h.discovered)
	r.Post("/api/cluster/join", h.join)
	r.Post("/api/cluster/dismiss", h.dismiss)
	r.Delete("/api/cluster/leave/{node_id}", h.leave)
	r.Get("/api/cluster/proxy/{node_id}/devices/{device_id}", h.proxyDevice)
	r.Post("/api/cluster/proxy/{node_id}/open", h.proxyOpen)
	r.Post("/api/cluster/proxy/{node_id}/sessions/{session_id}/close", h.proxyCloseSession)
	r.Delete("/api/cluster/proxy/{node_id}/devices/{device_id}", h.proxyDeleteDevice)
	r.Post("/api/cluster/proxy/{node_id}/from-template/{key}", h.proxyFromTemplate)
	r.Post("/api/cluster/batch/exec", h.batchExec)
	// 节点间内部
	r.Post("/api/cluster/internal/handshake", h.internalHandshake)
	r.Get("/api/cluster/internal/ping", h.internalPing)
	r.Get("/api/cluster/internal/directory", h.internalDirectory)
	r.Get("/api/cluster/internal/devices/{device_id}", h.internalDevice)
	r.Post("/api/cluster/internal/open", h.internalOpen)
	r.Post("/api/cluster/internal/sessions/{session_id}/close", h.internalCloseSession)
	r.Post("/api/cluster/internal/batch/exec", h.internalBatchExec)
	r.Post("/api/cluster/internal/devices/from-template/{key}", h.internalFromTemplate)
	r.Delete("/api/cluster/internal/devices/{device_id}", h.internalDeleteDevice)
	r.Get("/api/cluster/internal/connector-kinds", h.internalConnectorKinds)
}

func (h *ClusterHandler) requireEnabled(w http.ResponseWriter) bool {
	if h.cluster == nil {
		jsonErr(w, 400, "集群模式未启用")
		return false
	}
	return true
}

func (h *ClusterHandler) checkInternalToken(w http.ResponseWriter, r *http.Request) bool {
	if h.cluster == nil {
		jsonErr(w, 404, "集群模式未启用")
		return false
	}
	if r.Header.Get("X-LinkHub-Token") != h.cluster.Token() {
		jsonErr(w, 403, "令牌无效")
		return false
	}
	return true
}

// ---------- 身份/节点 ----------
func (h *ClusterHandler) info(w http.ResponseWriter, r *http.Request) {
	if h.cluster == nil {
		jsonOut(w, map[string]interface{}{
			"enabled": false, "node_id": "", "name": h.name, "address": h.address,
		})
		return
	}
	jsonOut(w, map[string]interface{}{
		"enabled": true, "node_id": h.cluster.SelfID(),
		"name": h.cluster.SelfName(), "address": h.cluster.Address(),
	})
}

func (h *ClusterHandler) nodes(w http.ResponseWriter, r *http.Request) {
	if h.cluster == nil {
		jsonOut(w, []interface{}{})
		return
	}
	jsonOut(w, h.cluster.ListNodes())
}

func (h *ClusterHandler) discovered(w http.ResponseWriter, r *http.Request) {
	if !h.requireEnabled(w) {
		return
	}
	jsonOut(w, h.cluster.ListPending())
}

func (h *ClusterHandler) join(w http.ResponseWriter, r *http.Request) {
	if !h.requireEnabled(w) {
		return
	}
	var body struct {
		Address string `json:"address"`
		Token   string `json:"token"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	n, err := h.cluster.Join(body.Address)
	if err != nil {
		jsonErr(w, 502, err.Error())
		return
	}
	jsonOut(w, n.Node)
}

func (h *ClusterHandler) dismiss(w http.ResponseWriter, r *http.Request) {
	if !h.requireEnabled(w) {
		return
	}
	var body struct {
		NodeIDs []string `json:"node_ids"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	for _, id := range body.NodeIDs {
		if id != h.cluster.SelfID() {
			h.cluster.RemovePeer(id)
			h.cluster.ForgetNode(id)
		}
	}
	h.cluster.DismissPending(body.NodeIDs)
	jsonOut(w, map[string]bool{"ok": true})
}

func (h *ClusterHandler) leave(w http.ResponseWriter, r *http.Request) {
	if !h.requireEnabled(w) {
		return
	}
	id := r.PathValue("node_id")
	if id == h.cluster.SelfID() {
		jsonErr(w, 400, "不能移除本机节点")
		return
	}
	h.cluster.RemovePeer(id)
	h.cluster.ForgetNode(id)
	jsonOut(w, map[string]bool{"ok": true})
}

// ---------- proxy（本前端 → 远程节点） ----------
func (h *ClusterHandler) peerOrErr(w http.ResponseWriter, nodeID string) *cluster.Node {
	p := h.cluster.GetPeer(nodeID)
	if p == nil || p.Status != "online" {
		jsonErr(w, 503, "节点不在线")
		return nil
	}
	return p
}

func (h *ClusterHandler) proxyDevice(w http.ResponseWriter, r *http.Request) {
	if !h.requireEnabled(w) {
		return
	}
	p := h.peerOrErr(w, r.PathValue("node_id"))
	if p == nil {
		return
	}
	var out struct {
		Device      models.Device            `json:"device"`
		Connections []models.ConnectionProfile `json:"connections"`
	}
	path := fmt.Sprintf("/api/cluster/internal/devices/%s", r.PathValue("device_id"))
	if err := h.cluster.GetJSON(p, path, &out); err != nil {
		jsonErr(w, 502, err.Error())
		return
	}
	jsonOut(w, out)
}

func (h *ClusterHandler) proxyOpen(w http.ResponseWriter, r *http.Request) {
	if !h.requireEnabled(w) {
		return
	}
	p := h.peerOrErr(w, r.PathValue("node_id"))
	if p == nil {
		return
	}
	var body struct {
		ConnectionID int64 `json:"connection_id"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	var out map[string]interface{}
	if err := h.cluster.PostJSON(p, "/api/cluster/internal/open", body, &out); err != nil {
		jsonErr(w, 409, err.Error())
		return
	}
	jsonOut(w, out)
}

func (h *ClusterHandler) proxyCloseSession(w http.ResponseWriter, r *http.Request) {
	if !h.requireEnabled(w) {
		return
	}
	p := h.peerOrErr(w, r.PathValue("node_id"))
	if p == nil {
		return
	}
	path := fmt.Sprintf("/api/cluster/internal/sessions/%s/close", r.PathValue("session_id"))
	var out map[string]interface{}
	if err := h.cluster.PostJSON(p, path, map[string]interface{}{}, &out); err != nil {
		jsonErr(w, 502, err.Error())
		return
	}
	jsonOut(w, out)
}

func (h *ClusterHandler) proxyDeleteDevice(w http.ResponseWriter, r *http.Request) {
	if !h.requireEnabled(w) {
		return
	}
	p := h.peerOrErr(w, r.PathValue("node_id"))
	if p == nil {
		return
	}
	path := fmt.Sprintf("/api/cluster/internal/devices/%s", r.PathValue("device_id"))
	if err := h.cluster.DeleteJSON(p, path); err != nil {
		jsonErr(w, 502, err.Error())
		return
	}
	w.WriteHeader(204)
}

func (h *ClusterHandler) proxyFromTemplate(w http.ResponseWriter, r *http.Request) {
	if !h.requireEnabled(w) {
		return
	}
	p := h.peerOrErr(w, r.PathValue("node_id"))
	if p == nil {
		return
	}
	var body map[string]interface{}
	json.NewDecoder(r.Body).Decode(&body)
	path := fmt.Sprintf("/api/cluster/internal/devices/from-template/%s", r.PathValue("key"))
	var out map[string]interface{}
	if err := h.cluster.PostJSON(p, path, body, &out); err != nil {
		jsonErr(w, 502, err.Error())
		return
	}
	jsonOut(w, out)
}

// batchExec 混合批量：本机直连，远程走 internal/batch/exec
func (h *ClusterHandler) batchExec(w http.ResponseWriter, r *http.Request) {
	if !h.requireEnabled(w) {
		return
	}
	var body struct {
		Targets []struct {
			NodeID   string `json:"node_id"`
			DeviceID int64  `json:"device_id"`
		} `json:"targets"`
		Command string `json:"command"`
		WaitMs  int    `json:"wait_ms"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.WaitMs < 100 {
		body.WaitMs = 1500
	}
	if body.WaitMs > 30000 {
		body.WaitMs = 30000
	}

	localIDs := []int64{}
	remote := map[string][]int64{}
	for _, t := range body.Targets {
		if t.NodeID == "" || t.NodeID == "local" || t.NodeID == h.cluster.SelfID() {
			localIDs = append(localIDs, t.DeviceID)
		} else {
			remote[t.NodeID] = append(remote[t.NodeID], t.DeviceID)
		}
	}

	results := []map[string]interface{}{}
	if len(localIDs) > 0 {
		results = append(results, h.localBatchExec(localIDs, body.Command, body.WaitMs)...)
	}
	for nodeID, ids := range remote {
		p := h.cluster.GetPeer(nodeID)
		if p == nil || p.Status != "online" {
			for _, did := range ids {
				results = append(results, map[string]interface{}{
					"node_id": nodeID, "device_id": did, "device_name": fmt.Sprintf("#%d", did),
					"ok": false, "output": "", "error": "节点不在线",
				})
			}
			continue
		}
		var out struct {
			Results []map[string]interface{} `json:"results"`
		}
		err := h.cluster.PostJSON(p, "/api/cluster/internal/batch/exec",
			map[string]interface{}{"device_ids": ids, "command": body.Command, "wait_ms": body.WaitMs}, &out)
		if err != nil {
			for _, did := range ids {
				results = append(results, map[string]interface{}{
					"node_id": nodeID, "device_id": did, "device_name": fmt.Sprintf("#%d", did),
					"ok": false, "output": "", "error": err.Error(),
				})
			}
			continue
		}
		results = append(results, out.Results...)
	}
	jsonOut(w, map[string]interface{}{"results": results})
}

// ---------- internal（节点间，X-LinkHub-Token） ----------
func (h *ClusterHandler) internalHandshake(w http.ResponseWriter, r *http.Request) {
	if !h.checkInternalToken(w, r) {
		return
	}
	var body struct {
		NodeID  string `json:"node_id"`
		Name    string `json:"name"`
		Address string `json:"address"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	h.cluster.Handshake(body.Address, body.Name, body.NodeID)
	jsonOut(w, map[string]interface{}{
		"node_id": h.cluster.SelfID(), "name": h.cluster.SelfName(),
		"address": h.cluster.Address(), "peers": h.cluster.ListNodes(),
	})
}

func (h *ClusterHandler) internalPing(w http.ResponseWriter, r *http.Request) {
	if !h.checkInternalToken(w, r) {
		return
	}
	jsonOut(w, map[string]interface{}{
		"node_id": h.cluster.SelfID(), "name": h.cluster.SelfName(),
		"address": h.cluster.Address(), "ts": time.Now().Format(time.RFC3339),
		"resources": h.cluster.SelfResources(),
	})
}

// internalDirectory 本机设备目录（反熵数据源）
func (h *ClusterHandler) internalDirectory(w http.ResponseWriter, r *http.Request) {
	if !h.checkInternalToken(w, r) {
		return
	}
	devices, _, _ := h.store.ListDevices("", "", nil)
	online := h.manager.OnlineDeviceIDs()
	out := []cluster.RemoteDevice{}
	for _, d := range devices {
		d.Online = online[d.ID]
		d.NodeID = "local"
		conns, _ := h.store.ListConnections(d.ID)
		rcs := []cluster.RemoteConn{}
		for _, c := range conns {
			rcs = append(rcs, cluster.RemoteConn{
				ID: c.ID, Kind: c.Kind, Name: c.Name, NodeID: c.NodeID, Enabled: c.Enabled, Params: c.Params,
			})
		}
		out = append(out, cluster.RemoteDevice{Device: d, Connections: rcs})
	}
	jsonOut(w, out)
}

func (h *ClusterHandler) internalDevice(w http.ResponseWriter, r *http.Request) {
	if !h.checkInternalToken(w, r) {
		return
	}
	id, err := paramID(r, "device_id")
	if err != nil {
		jsonErr(w, 400, "设备 ID 无效")
		return
	}
	d, _ := h.store.GetDevice(id)
	if d == nil {
		jsonErr(w, 404, "设备不存在")
		return
	}
	d.Online = h.manager.OnlineDeviceIDs()[d.ID]
	conns, _ := h.store.ListConnections(id)
	jsonOut(w, map[string]interface{}{"device": d, "connections": conns})
}

func (h *ClusterHandler) internalOpen(w http.ResponseWriter, r *http.Request) {
	if !h.checkInternalToken(w, r) {
		return
	}
	var body struct {
		ConnectionID int64                  `json:"connection_id"`
		Kind         string                 `json:"kind"`
		Params       map[string]interface{} `json:"params"`
		DeviceID     int64                  `json:"device_id"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.ConnectionID > 0 {
		c, _ := h.store.GetConnection(body.ConnectionID)
		if c == nil {
			jsonErr(w, 404, "连接配置不存在")
			return
		}
		s, err := h.manager.Open(c.ID, c.Kind, c.Params, c.DeviceID, "cluster")
		respondOpenResult(w, s, err, h.cluster.SelfID())
		return
	}
	// 内联参数打开（无落库连接配置）
	s, err := h.manager.Open(0, body.Kind, body.Params, body.DeviceID, "cluster")
	respondOpenResult(w, s, err, h.cluster.SelfID())
}

func (h *ClusterHandler) internalCloseSession(w http.ResponseWriter, r *http.Request) {
	if !h.checkInternalToken(w, r) {
		return
	}
	id, err := paramID(r, "session_id")
	if err != nil {
		jsonErr(w, 400, "会话 ID 无效")
		return
	}
	if err := h.manager.Close(id); err != nil {
		jsonErr(w, 404, err.Error())
		return
	}
	jsonOut(w, map[string]bool{"closed": true})
}

func (h *ClusterHandler) internalBatchExec(w http.ResponseWriter, r *http.Request) {
	if !h.checkInternalToken(w, r) {
		return
	}
	var body struct {
		DeviceIDs []int64 `json:"device_ids"`
		Command   string  `json:"command"`
		WaitMs    int     `json:"wait_ms"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.WaitMs < 100 {
		body.WaitMs = 1500
	}
	if body.WaitMs > 30000 {
		body.WaitMs = 30000
	}
	jsonOut(w, map[string]interface{}{"results": h.localBatchExec(body.DeviceIDs, body.Command, body.WaitMs)})
}

func (h *ClusterHandler) internalFromTemplate(w http.ResponseWriter, r *http.Request) {
	if !h.checkInternalToken(w, r) {
		return
	}
	key := r.PathValue("key")
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
		Description: fmt.Sprintf("基于模板「%s」创建", tpl.Name), NodeID: h.cluster.SelfID()}
	if err := h.store.CreateDevice(&d); err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	for _, conn := range tpl.DefaultConnections {
		kind, _ := conn["kind"].(string)
		name, _ := conn["name"].(string)
		params, _ := conn["params"].(map[string]interface{})
		// param_overrides 覆盖：键为连接序号字符串
		h.store.CreateConnection(&models.ConnectionProfile{
			DeviceID: d.ID, Kind: kind, Name: name, Params: params, Enabled: true, NodeID: "local",
		})
	}
	jsonOut(w, map[string]interface{}{"id": d.ID, "name": d.Name})
}

func (h *ClusterHandler) internalDeleteDevice(w http.ResponseWriter, r *http.Request) {
	if !h.checkInternalToken(w, r) {
		return
	}
	id, err := paramID(r, "device_id")
	if err != nil {
		jsonErr(w, 400, "设备 ID 无效")
		return
	}
	if d, _ := h.store.GetDevice(id); d == nil {
		jsonErr(w, 404, "设备不存在")
		return
	}
	cascadeDeleteDevice(h.store, h.manager, id)
	w.WriteHeader(204)
}

func (h *ClusterHandler) internalConnectorKinds(w http.ResponseWriter, r *http.Request) {
	if !h.checkInternalToken(w, r) {
		return
	}
	jsonOut(w, connectorKindList())
}

// ---------- 本机批量执行（automation 与 internal 共用） ----------
func (h *ClusterHandler) localBatchExec(deviceIDs []int64, command string, waitMs int) []map[string]interface{} {
	out := []map[string]interface{}{}
	for _, did := range deviceIDs {
		dev, _ := h.store.GetDevice(did)
		name := fmt.Sprintf("#%d", did)
		if dev != nil {
			name = dev.Name
		}
		conns, _ := h.store.ListConnections(did)
		var conn *models.ConnectionProfile
		for i := range conns {
			if conns[i].Enabled {
				conn = &conns[i]
				break
			}
		}
		if conn == nil {
			out = append(out, map[string]interface{}{"node_id": "local", "device_id": did, "device_name": name,
				"ok": false, "output": "", "error": "无启用的连接配置"})
			continue
		}
		out = append(out, execOnConn(h.manager, conn, command, waitMs, "local", did, name))
	}
	return out
}

// execOnConn 开临时会话执行命令并收集输出窗口
func execOnConn(mgr *session.Manager, conn *models.ConnectionProfile, command string, waitMs int, nodeID string, deviceID int64, deviceName string) map[string]interface{} {
	sess, err := mgr.Open(conn.ID, conn.Kind, conn.Params, conn.DeviceID, "batch")
	if err != nil {
		return map[string]interface{}{"node_id": nodeID, "device_id": deviceID, "device_name": deviceName,
			"ok": false, "output": "", "error": err.Error()}
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && sess.Status != "online" {
		if sess.Status == "error" {
			mgr.Close(sess.ID)
			return map[string]interface{}{"node_id": nodeID, "device_id": deviceID, "device_name": deviceName,
				"ok": false, "output": "", "error": sess.LastError}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if sess.Status != "online" {
		mgr.Close(sess.ID)
		return map[string]interface{}{"node_id": nodeID, "device_id": deviceID, "device_name": deviceName,
			"ok": false, "output": "", "error": "会话上线超时"}
	}
	client, _ := sess.Subscribe("batch", "批量执行")
	sess.Write("", []byte(command+"\r"))
	output := []byte{}
	timeout := time.After(time.Duration(waitMs) * time.Millisecond)
loop:
	for {
		select {
		case item := <-client.Queue:
			if b, ok := item.([]byte); ok {
				output = append(output, b...)
			}
		case <-timeout:
			break loop
		}
	}
	sess.Unsubscribe("batch")
	mgr.Close(sess.ID)
	return map[string]interface{}{"node_id": nodeID, "device_id": deviceID, "device_name": deviceName,
		"ok": true, "output": string(output), "error": ""}
}

// cascadeDeleteDevice 级联删除：关会话 → 删日志 → 删连接 → 删设备
func cascadeDeleteDevice(st *store.Store, mgr *session.Manager, deviceID int64) {
	conns, _ := st.ListConnections(deviceID)
	for _, c := range conns {
		for _, sid := range st.SessionIDsByConnection(c.ID) {
			mgr.Close(sid)
		}
	}
	st.DeleteDevice(deviceID)
}

// connectorKindList 连接器类型与参数 JSON Schema（与 Python 版同构，前端 ConnectionForm 按
// properties.{key}.type/title/default/enum 动态渲染表单）
func connectorKindList() []map[string]interface{} {
	schemas := map[string]map[string]interface{}{
		"serial": {
			"required": []string{"port"},
			"properties": map[string]interface{}{
				"port":     map[string]interface{}{"type": "string", "title": "串口设备", "description": "如 /dev/ttyUSB0、/dev/ttyACM0、COM3"},
				"baudrate": map[string]interface{}{"type": "integer", "title": "波特率", "default": 115200},
				"bytesize": map[string]interface{}{"type": "integer", "title": "数据位", "default": 8, "enum": []int{5, 6, 7, 8}},
				"parity":   map[string]interface{}{"type": "string", "title": "校验", "default": "N", "enum": []string{"N", "E", "O", "M", "S"}},
				"stopbits": map[string]interface{}{"type": "integer", "title": "停止位", "default": 1, "enum": []int{1, 2}},
				"local_echo": map[string]interface{}{"type": "boolean", "title": "本地回显", "default": false,
					"description": "对无回显设备（如裸串口）开启"},
			},
		},
		"ssh": {
			"required": []string{"host"},
			"properties": map[string]interface{}{
				"host":     map[string]interface{}{"type": "string", "title": "主机", "description": "设备 IP 或主机名"},
				"port":     map[string]interface{}{"type": "integer", "title": "端口", "default": 22},
				"username": map[string]interface{}{"type": "string", "title": "用户名", "default": "root"},
				"password": map[string]interface{}{"type": "string", "title": "密码", "default": "", "description": "SSH 密码认证"},
			},
		},
		"telnet": {
			"required": []string{"host"},
			"properties": map[string]interface{}{
				"host": map[string]interface{}{"type": "string", "title": "主机", "default": ""},
				"port": map[string]interface{}{"type": "integer", "title": "端口", "default": 23},
				"connect_timeout": map[string]interface{}{"type": "integer", "title": "连接超时(秒)", "default": 10},
			},
		},
		"mqtt": {
			"required": []string{"host"},
			"properties": map[string]interface{}{
				"host":     map[string]interface{}{"type": "string", "title": "Broker 主机", "default": ""},
				"port":     map[string]interface{}{"type": "integer", "title": "端口", "default": 1883},
				"username": map[string]interface{}{"type": "string", "title": "用户名", "default": ""},
				"password": map[string]interface{}{"type": "string", "title": "密码", "default": ""},
				"topic_sub": map[string]interface{}{"type": "string", "title": "订阅主题", "default": "#",
					"description": "终端里显示这些主题的消息"},
				"topic_pub": map[string]interface{}{"type": "string", "title": "发布主题", "default": "cmd",
					"description": "终端里输入的行发布到该主题"},
			},
		},
	}
	out := []map[string]interface{}{}
	for _, k := range connector.Kinds() {
		schema, ok := schemas[k]
		if !ok {
			schema = map[string]interface{}{"properties": map[string]interface{}{}}
		}
		out = append(out, map[string]interface{}{"kind": k, "schema": schema})
	}
	return out
}
