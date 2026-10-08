package api

import (
	"encoding/json"
	"net/http"

	"linkhub/internal/cluster"
)

// ClusterHandlers 集群相关接口
type ClusterHandler struct {
	cluster *cluster.Cluster
	store   interface{} // 复用 api.go 的 store
}

func NewClusterHandler(c *cluster.Cluster) *ClusterHandler {
	return &ClusterHandler{cluster: c}
}

func (h *ClusterHandler) RegisterRoutes(r interface {
	Post(string, http.HandlerFunc)
	Get(string, http.HandlerFunc)
}) {
	r.Post("/api/cluster/join", h.join)
	r.Get("/api/cluster/nodes", h.nodes)
	r.Post("/api/cluster/internal/handshake", h.internalHandshake)
	r.Get("/api/cluster/internal/ping", h.internalPing)
	r.Get("/api/cluster/internal/directory", h.internalDirectory)
}

func (h *ClusterHandler) join(w http.ResponseWriter, r *http.Request) {
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

func (h *ClusterHandler) nodes(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, h.cluster.ListNodes())
}

func (h *ClusterHandler) internalHandshake(w http.ResponseWriter, r *http.Request) {
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
	if r.Header.Get("X-LinkHub-Token") != h.cluster.Token() {
		jsonErr(w, 403, "令牌无效")
		return
	}
	// 返回本机设备目录（简化：只列设备摘要）
	jsonOut(w, []interface{}{})
}

func now() string { return "" } // RFC3339 由调用方填
