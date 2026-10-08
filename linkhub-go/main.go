package main

import (
	"crypto/rand"
	"fmt"
	"log"
	"net/http"
	"path/filepath"

	"github.com/go-chi/chi/v5"

	"linkhub/internal/api"
	"linkhub/internal/auth"
	"linkhub/internal/cluster"
	"linkhub/internal/config"
	"linkhub/internal/session"
	"linkhub/internal/store"
	"linkhub/internal/ws"
)

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

func main() {
	cfg := config.Load()
	dbPath := filepath.Join(cfg.DataDir, "linkhub.db")
	st, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("数据库打开失败: %v", err)
	}
	defer st.Close()

	// 内置模板同步
	if err := syncTemplates(st); err != nil {
		log.Printf("模板同步: %v", err)
	}

	// 默认管理员
	st.SetMeta("secret_key", cfg.SecretKey)
	if err := auth.EnsureDefaultAdmin(st); err != nil {
		log.Printf("默认管理员: %v", err)
	}

	mgr := session.NewManager(st)
	var clusterRelay *ws.RelayHandler
	mw := auth.NewMiddleware(st, cfg.SecretKey)
	crypto := auth.NewCrypto(cfg.SecretKey)

	h := api.NewHandler(st, mgr, mw, crypto)
	wsH := ws.NewHandler(mgr)

	r := chi.NewRouter()
	// 直接挂 API 路由（不用 Mount，避免路径前缀问题）
	r.Mount("/", h.Router())
	r.Get("/ws/terminal/{session_id}", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("node") != "" {
			// 中继（集群模式才注册 relay，未启用时 404）
			if clusterRelay != nil {
				clusterRelay.RelayTerminal(w, r)
				return
			}
			http.NotFound(w, r)
			return
		}
		wsH.Terminal(w, r)
	})
	r.Get("/ws/events", wsH.Events)

	// 集群（启用时）
	if cfg.ClusterEnabled {
		nodeID := st.GetMeta("node_id")
		if nodeID == "" {
			nodeID = "n-" + randHex(4)
			st.SetMeta("node_id", nodeID)
		}
		adv := cfg.AdvertiseAddr
		if adv == "" {
			adv = fmt.Sprintf("http://127.0.0.1:%d", cfg.Port)
		}
		cl := cluster.NewCluster(st, nodeID, cfg.NodeName, adv, cfg.ClusterToken)
		ch := api.NewClusterHandler(cl)
		ch.RegisterRoutes(r)
		// 终端中继（跨节点）
		relay := ws.NewRelayHandler(mgr, cl)
		clusterRelay = relay
		r.Get("/ws/cluster/relay/{session_id}", relay.PeerRelay)
		go cl.DirectPeersLoop(cfg.ClusterPeers)
		if cfg.DiscoveryPort > 0 {
			go cl.BeaconLoop(cfg.DiscoveryPort, []string{"255.255.255.255"})
		}
		log.Printf("集群模式: %s @ %s (node_id=%s)", cfg.NodeName, adv, nodeID)
	}

	// 前端静态托管（embed 嵌入二进制，单文件分发）
	r.Get("/*", func(w http.ResponseWriter, r *http.Request) { api.FrontendHandler().ServeHTTP(w, r) })

	addr := fmt.Sprintf(":%d", cfg.Port)
	log.Printf("🐙 灵枢 LinkHub 启动 http://0.0.0.0:%d", cfg.Port)
	log.Printf("数据目录: %s", cfg.DataDir)
	if cfg.ClusterEnabled {
		log.Printf("集群模式: %s @ %s", cfg.NodeName, cfg.AdvertiseAddr)
	}
	log.Fatal(http.ListenAndServe(addr, r))
}

func syncTemplates(st *store.Store) error {
	count, err := st.SyncBuiltinTemplates("templates_builtin")
	if err == nil {
		log.Printf("内置模板同步完成，共 %d 个", count)
	}
	return err
}
