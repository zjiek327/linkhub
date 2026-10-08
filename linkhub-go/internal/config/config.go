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
	ClusterEnabled bool
	DiscoveryPort int
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func Load() *Config {
	port, _ := strconv.Atoi(envOr("LINKHUB_PORT", "8000"))
	discPort, _ := strconv.Atoi(envOr("LINKHUB_DISCOVERY_PORT", "37890"))
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
	return &Config{
		Port:          port,
		DataDir:       dataDir,
		SecretKey:     envOr("LINKHUB_SECRET_KEY", "linkhub-dev-secret-change-me"),
		ClusterToken:  envOr("LINKHUB_CLUSTER_TOKEN", ""),
		NodeName:      envOr("LINKHUB_NODE_NAME", ""),
		AdvertiseAddr: envOr("LINKHUB_ADVERTISE_ADDR", ""),
		ClusterPeers:  peers,
		ClusterEnabled: envOr("LINKHUB_CLUSTER_ENABLED", "false") == "true",
		DiscoveryPort: discPort,
	}
}
