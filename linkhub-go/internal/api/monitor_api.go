package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"linkhub/internal/cluster"
	"linkhub/internal/monitor"
)

// RegisterMonitorRoutes 流量监控 API（monitorService nil=未启用）
//   GET /api/monitor/traffic?node=&window=30m  实时快照+分钟桶历史（node 空=本机，远程经集群转发）
//   GET /api/cluster/internal/monitor/traffic   集群转发落点（X-LinkHub-Token）
func RegisterMonitorRoutes(r chi.Router, m *monitor.Service, cl *cluster.Cluster, mw interface {
	User(next http.Handler) http.Handler
}) {
	h := &monitorHandler{monitor: m, cluster: cl}
	r.Route("/api/monitor", func(ar chi.Router) {
		ar.Use(mw.User)
		ar.Get("/traffic", h.traffic)
	})
	if cl != nil {
		r.Get("/api/cluster/internal/monitor/traffic", h.internalTraffic)
	}
}

type monitorHandler struct {
	monitor *monitor.Service
	cluster *cluster.Cluster
}

func (h *monitorHandler) traffic(w http.ResponseWriter, r *http.Request) {
	node := r.URL.Query().Get("node")
	window, _ := time.ParseDuration(r.URL.Query().Get("window"))
	if window <= 0 {
		window = 30 * time.Minute
	}
	if node == "" || h.cluster == nil || node == h.cluster.SelfID() {
		h.local(w, window)
		return
	}
	// 直连 peer 调用（不经 internal/forward 链：两节点互为 gossip 间接节点时
	// CallAny 会走 forward 环导致「转发跳数超限」；监控数据高频，直连失败即报错）
	peer := h.cluster.GetPeer(node)
	if peer == nil || peer.Status != "online" {
		jsonErr(w, 502, "节点不可达或为间接节点（监控暂只支持直连节点）")
		return
	}
	var out map[string]interface{}
	if err := h.cluster.GetJSON(peer,
		"/api/cluster/internal/monitor/traffic?window="+r.URL.Query().Get("window"), &out); err != nil {
		jsonErr(w, 502, "获取远程节点流量失败: "+err.Error())
		return
	}
	jsonOut(w, out)
}

func (h *monitorHandler) internalTraffic(w http.ResponseWriter, r *http.Request) {
	if h.cluster == nil || r.Header.Get("X-LinkHub-Token") != h.cluster.Token() {
		jsonErr(w, 403, "令牌无效")
		return
	}
	window, _ := time.ParseDuration(r.URL.Query().Get("window"))
	if window <= 0 {
		window = 30 * time.Minute
	}
	h.local(w, window)
}

func (h *monitorHandler) local(w http.ResponseWriter, window time.Duration) {
	if h.monitor == nil || !h.monitor.Enabled() {
		jsonErr(w, 503, "流量监控未启用（需以 root/CAP_BPF 运行后端）")
		return
	}
	jsonOut(w, map[string]interface{}{
		"snapshot": h.monitor.Snapshot(),
		"history":  h.monitor.History(window),
	})
}
