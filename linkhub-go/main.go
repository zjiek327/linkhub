package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"time"

	"github.com/go-chi/chi/v5"

	"linkhub/internal/api"
	"linkhub/internal/auth"
	"linkhub/internal/automation"
	"linkhub/internal/cluster"
	"linkhub/internal/config"
	"linkhub/internal/desktop"
	"linkhub/internal/events"
	"linkhub/internal/monitor"
	"linkhub/internal/session"
	"linkhub/internal/store"
	"linkhub/internal/ws"
)

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

// primaryLANAddr 推导集群广播地址：UDP 假连做路由查询（不产生实际流量）取出口 IP，
// 失败则遍历网卡取第一个非回环 IPv4，最终回退 127.0.0.1
func primaryLANAddr(port int) string {
	if conn, err := net.Dial("udp", "198.51.100.1:80"); err == nil {
		defer conn.Close()
		if a, ok := conn.LocalAddr().(*net.UDPAddr); ok && !a.IP.IsLoopback() && a.IP.To4() != nil {
			return fmt.Sprintf("http://%s:%d", a.IP.String(), port)
		}
	}
	if ifs, err := net.Interfaces(); err == nil {
		for _, ifi := range ifs {
			if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagLoopback != 0 {
				continue
			}
			addrs, _ := ifi.Addrs()
			for _, a := range addrs {
				if ipnet, ok := a.(*net.IPNet); ok {
					if ip := ipnet.IP.To4(); ip != nil && !ip.IsLoopback() {
						return fmt.Sprintf("http://%s:%d", ip.String(), port)
					}
				}
			}
		}
	}
	return fmt.Sprintf("http://127.0.0.1:%d", port)
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

	// 上次残留的开启会话标记关闭（防僵尸）
	st.MarkAllSessionsClosed()

	mgr := session.NewManager(st)
	mw := auth.NewMiddleware(st, cfg.SecretKey)
	crypto := auth.NewCrypto(cfg.SecretKey)
	bus := events.NewBus()

	// 会话状态 → 事件总线（前端 /ws/events）
	mgr.OnStatus = func(sessionID, deviceID int64, status, errMsg string) {
		bus.Publish("session_status", map[string]interface{}{
			"session_id": sessionID, "device_id": deviceID, "status": status,
			"error": errMsg, "ts": time.Now().Format(time.RFC3339),
			"device_online": status == "online",
		})
	}

	// 集群（启用时创建实例；/api/cluster/* 始终注册，未启用时返回 enabled=false）
	var cl *cluster.Cluster
	if cfg.ClusterEnabled {
		nodeID := st.GetMeta("node_id")
		if nodeID == "" {
			nodeID = "n-" + randHex(4)
			st.SetMeta("node_id", nodeID)
		}
		adv := cfg.AdvertiseAddr
		if adv == "" {
			// 默认 127.0.0.1 会让其他节点拿着回环地址来连（发现成功但握手必败），自动推导 LAN IP
			adv = primaryLANAddr(cfg.Port)
			log.Printf("未配置 LINKHUB_ADVERTISE_ADDR，自动推导广播地址: %s（多网卡机器若不正确请显式配置）", adv)
		}
		cl = cluster.NewCluster(st, nodeID, cfg.NodeName, adv, cfg.ClusterToken)
		cl.SetDesktopPort(cfg.DesktopPort)
		// 存量设备归属迁移：本机设备必须带本节点 ID，否则前端会误判为远程副本
		st.Exec(nil, `UPDATE devices SET node_id=? WHERE node_id='local' OR node_id=''`, nodeID)
		// 节点状态 → 事件总线
		cl.OnEvent(func(event string, data map[string]interface{}) {
			bus.Publish(event, data)
		})
		log.Printf("集群模式: %s @ %s (node_id=%s)", cfg.NodeName, adv, nodeID)
	}

	// 自动化（定时任务调度，独立于集群）
	sched := automation.NewScheduler(st, mgr, cl, bus)
	sched.Start()

	// 流量监控（eBPF kretprobe；非 root 运行时优雅禁用，不影响其他功能）
	mon := monitor.Start()
	defer mon.Close()
	log.Printf("流量监控: %s", map[bool]string{true: "已启用（eBPF 采集）", false: mon.Reason()}[mon.Enabled()])

	// 系统指标监控（gopsutil，全平台无 root 要求）
	metricsSvc := monitor.StartMetrics(func() int { return len(mgr.OnlineSessionIDs()) })
	defer metricsSvc.Close()

	h := api.NewHandler(st, mgr, mw, crypto, bus, cl, sched)
	wsH := ws.NewHandler(mgr, bus)
	// 终端协作边界：WS 查询 token 解析租户（与 auth 中间件同源逻辑）
	wsH.SetTenantResolver(func(r *http.Request) *int64 {
		ctx, reject := mw.Authenticate(r)
		if reject != "" {
			return nil
		}
		tc := auth.TenantFrom(ctx)
		if tc == nil || tc.TenantID == 0 {
			return nil
		}
		id := tc.TenantID
		return &id
	})
	var clusterRelay *ws.RelayHandler
	if cl != nil {
		clusterRelay = ws.NewRelayHandler(mgr, cl)
	}

	r := chi.NewRouter()
	// 租户/设备组路由先注册（具体前缀优先于下方的 Mount("/",...) 全量挂载）
	h.RegisterTenantRoutes(r)
	h.RegisterDeviceGroupRoutes(r)
	h.RegisterSFTPRoutes(r)
	// 直接挂 API 路由（不用 Mount，避免路径前缀问题）
	r.Mount("/", h.Router())
	r.Get("/ws/terminal/{session_id}", func(w http.ResponseWriter, r *http.Request) {
		node := r.URL.Query().Get("node")
		// node 为空或等于本节点 → 本地终端；否则经中继（间接节点链式转发）
		if node != "" && node != cl.SelfID() {
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

	// 节点桌面共享（VNC/RFB）：本机代理 + 跨节点中继，分发逻辑在 handler 内按 node 参数判断
	deskH := &ws.DesktopHandler{
		Port:    cfg.DesktopPort,
		Cluster: cl,
		Auth: func(r *http.Request) bool {
			_, reject := mw.Authenticate(r)
			return reject == ""
		},
	}
	r.Route("/api/desktop", func(ar chi.Router) {
		ar.Use(mw.User)
		ar.Get("/capability", deskH.Capability)
		ar.Post("/setup", deskH.Setup)
	})
	r.Get("/ws/desktop", deskH.WS)
	// 节点间内部：桌面安装辅助的落点（集群令牌校验，转发链路见 internal/forward）
	if cl != nil {
		r.Post("/api/cluster/internal/desktop/setup", func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-LinkHub-Token") != cl.Token() {
				http.Error(w, "令牌无效", http.StatusForbidden)
				return
			}
			var body struct {
				Password string `json:"password"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(desktop.Setup(cfg.DesktopPort, body.Password))
		})
	}

	if cl != nil {
		r.Get("/ws/cluster/relay/{session_id}", clusterRelay.PeerRelay)
		go cl.DirectPeersLoop(cfg.ClusterPeers)
		if cfg.DiscoveryPort > 0 {
			// 广播地址动态读取（设置页「发现网络」保存后下一轮生效）
			go cl.BeaconLoop(cfg.DiscoveryPort, func() []string {
				if v := st.GetMeta("beacon_interfaces"); v != "" {
					list := []string{}
					json.Unmarshal([]byte(v), &list)
					if len(list) > 0 {
						return list
					}
				}
				return api.DefaultBeaconBroadcasts()
			}, cfg.ClusterSeeds)
		}
		go cl.DirectorySyncLoop()
		go cl.ResourceWatchLoop(func(event string, data map[string]interface{}) { bus.Publish(event, data) })
	}

	ch := api.NewClusterHandler(cl, st, mgr, cfg.ClusterEnabled, cfg.NodeName, cfg.AdvertiseAddr)
	ch.RegisterRoutes(r)

	// 前端静态托管（embed 嵌入二进制，单文件分发）
	// 用 NotFound 兜底而非 r.Get("/*")：chi 中 /* 端点会整个吞掉 Mount("/", api) 的子路由
	api.RegisterMonitorRoutes(r, mon, cl, mw, metricsSvc)
	r.NotFound(api.FrontendHandler().ServeHTTP)

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
