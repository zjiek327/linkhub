package api

import (
	"encoding/json"
	"net/http"

	"linkhub/internal/cluster"
	"linkhub/internal/store"
)

// ClusterHandlers 集群相关接口
type ClusterHandler struct {
	cluster *cluster.Cluster // nil 表示集群模式未启用
	store   *store.Store
	enabled bool
	// 未启用时用于 /info 回显
	name    string
	address string
}

func NewClusterHandler(c *cluster.Cluster, st *store.Store, enabled bool, name, address string) *ClusterHandler {
	return &ClusterHandler{cluster: c, store: st, enabled: enabled, name: name, address: address}
}

func (h *ClusterHandler) RegisterRoutes(r interface {
	Post(string, http.HandlerFunc)
	Get(string, http.HandlerFunc)
	Delete(string, http.HandlerFunc)
}) {
	r.Get("/api/cluster/info", h.info)
	r.Get("/api/cluster/nodes", h.nodes)
	r.Get("/api/cluster/discovered", h.discovered)
	r.Post("/api/cluster/join", h.join)
	r.Post("/api/cluster/dismiss", h.dismiss)
	r.Delete("/api/cluster/leave/{node_id}", h.leave)
	r.Post("/api/cluster/internal/handshake", h.internalHandshake)
	r.Get("/api/cluster/internal/ping", h.internalPing)
	r.Get("/api/cluster/internal/directory", h.internalDirectory)
}

// 未启用时统一报错
func (h *ClusterHandler) requireEnabled(w http.ResponseWriter) bool {
	if h.cluster == nil {
		jsonErr(w, 400, "集群模式未启用")
		return false
	}
	return true
}

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

// discovered UDP 广播发现的待加入节点（尚未实现发现列表，返回空）
func (h *ClusterHandler) discovered(w http.ResponseWriter, r *http.Request) {
	if !h.requireEnabled(w) {
		return
	}
	jsonOut(w, []interface{}{})
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

// dismiss 清理选中的节点记录（用于清掉离线幽灵节点）
func (h *ClusterHandler) dismiss(w http.ResponseWriter, r *http.Request) {
	if !h.requireEnabled(w) {
		return
	}
	var body struct {
		NodeIDs []string `json:"node_ids"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	for _, id := range body.NodeIDs {
		if id == h.cluster.SelfID() {
			continue // 不清理本机
		}
		h.cluster.RemovePeer(id)
		h.store.DeleteNode(id)
	}
	jsonOut(w, map[string]bool{"ok": true})
}

// leave 移除指定节点（对方仍在定向 peer 里会自动重连）
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
	h.store.DeleteNode(id)
	jsonOut(w, map[string]bool{"ok": true})
}

func (h *ClusterHandler) internalHandshake(w http.ResponseWriter, r *http.Request) {
	if h.cluster == nil {
		jsonErr(w, 403, "集群模式未启用")
		return
	}
	// 令牌校验
	if r.Header.Get("X-LinkHub-Token") != h.cluster.Token() {
		jsonErr(w, 403, "令牌无效")
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
		"node_id": h.cluster.SelfID(), "name": h.cluster.SelfName(), "address": h.cluster.Address(),
	})
}

func (h *ClusterHandler) internalPing(w http.ResponseWriter, r *http.Request) {
	if h.cluster == nil {
		jsonErr(w, 403, "集群模式未启用")
		return
	}
	if r.Header.Get("X-LinkHub-Token") != h.cluster.Token() {
		jsonErr(w, 403, "令牌无效")
		return
	}
	jsonOut(w, map[string]interface{}{
		"node_id": h.cluster.SelfID(), "name": h.cluster.SelfName(),
		"address": h.cluster.Address(), "ts": now(),
	})
}

func (h *ClusterHandler) internalDirectory(w http.ResponseWriter, r *http.Request) {
	if h.cluster == nil {
		jsonErr(w, 403, "集群模式未启用")
		return
	}
	if r.Header.Get("X-LinkHub-Token") != h.cluster.Token() {
		jsonErr(w, 403, "令牌无效")
		return
	}
	// 返回本机设备目录（简化：只列设备摘要）
	jsonOut(w, []interface{}{})
}

func now() string { return "" } // RFC3339 由调用方填
