package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// 全局配置：环境变量 LINKHUB_* 覆盖
type Config struct {
	Port          int
	DataDir       string
	SecretKey     string
	ClusterToken  string
	NodeName      string
	AdvertiseAddr string
	ClusterPeers  []string
	// ClusterSeeds 静态种子节点（跨网段发现用，UDP 单播，IP/host 可带 :port）
	ClusterSeeds  []string
	ClusterEnabled bool
	DiscoveryPort int
	DesktopPort   int // 节点桌面共享：本机 VNC 服务端端口（RFB）
}

func envOr(key, def string) string {
	// TrimSpace 兼容 Windows .env 的 CRLF 行尾（"true\r" 会导致比较失败）
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func Load() *Config {
	port, _ := strconv.Atoi(envOr("LINKHUB_PORT", "8000"))
	discPort, _ := strconv.Atoi(envOr("LINKHUB_DISCOVERY_PORT", "37890"))
	deskPort, _ := strconv.Atoi(envOr("LINKHUB_DESKTOP_PORT", "5900"))
	dataDir := envOr("LINKHUB_DATA_DIR", "")
	if dataDir == "" {
		if runtime.GOOS == "windows" {
			dataDir = filepath.Join(os.Getenv("APPDATA"), "LinkHub")
		} else {
			dataDir = filepath.Join(os.Getenv("HOME"), ".linkhub")
		}
	}
	os.MkdirAll(dataDir, 0o755)
	peers := []string{}
	if v := os.Getenv("LINKHUB_CLUSTER_PEERS"); v != "" {
		for _, p := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' }) {
			if p = strings.TrimSpace(p); p != "" {
				peers = append(peers, p)
			}
		}
	}
	seeds := []string{}
	if v := os.Getenv("LINKHUB_CLUSTER_SEEDS"); v != "" {
		for _, s := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' }) {
			if s = strings.TrimSpace(s); s != "" {
				seeds = append(seeds, s)
			}
		}
	}
	return &Config{
		Port:          port,
		DataDir:       dataDir,
		SecretKey:     envOr("LINKHUB_SECRET_KEY", "linkhub-dev-secret-change-me"),
		ClusterToken:  envOr("LINKHUB_CLUSTER_TOKEN", ""),
		NodeName:      envOr("LINKHUB_NODE_NAME", ""),
		AdvertiseAddr: envOr("LINKHUB_ADVERTISE_ADDR", ""),
		ClusterPeers:  peers,
		ClusterSeeds:  seeds,
		ClusterEnabled: envOr("LINKHUB_CLUSTER_ENABLED", "false") == "true",
		DiscoveryPort: discPort,
		DesktopPort:   deskPort,
	}
}
