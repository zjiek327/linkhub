package cluster

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"linkhub/internal/connector"
	"linkhub/internal/models"
)

// ---------- 待加入节点（UDP 广播发现） ----------
type Pending struct {
	NodeID  string `json:"node_id"`
	Name    string `json:"name"`
	Address string `json:"address"`
}

func (c *Cluster) AddPending(nodeID, name, address string) {
	c.mu.Lock()
	c.pending[nodeID] = Pending{nodeID, name, address}
	c.mu.Unlock()
}

func (c *Cluster) ListPending() []Pending {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := []Pending{}
	for _, p := range c.pending {
		out = append(out, p)
	}
	return out
}

// DismissPending 永久忽略（记入 meta，重启不复发）；返回 false 表示不在列表
func (c *Cluster) DismissPending(nodeIDs []string) {
	c.mu.Lock()
	for _, id := range nodeIDs {
		delete(c.pending, id)
	}
	c.mu.Unlock()
	dismissed := c.dismissedSet()
	for _, id := range nodeIDs {
		dismissed[id] = true
	}
	b, _ := json.Marshal(dismissed)
	c.store.SetMeta("dismissed_nodes", string(b))
}

// Join 清除对应 pending
func (c *Cluster) noteJoined(nodeID string) {
	c.mu.Lock()
	delete(c.pending, nodeID)
	c.mu.Unlock()
}

func (c *Cluster) dismissedSet() map[string]bool {
	out := map[string]bool{}
	if v := c.store.GetMeta("dismissed_nodes"); v != "" {
		json.Unmarshal([]byte(v), &out)
	}
	return out
}

func (c *Cluster) IsDismissed(nodeID string) bool {
	return c.dismissedSet()[nodeID]
}

// ---------- 节点间 HTTP 调用 ----------
func (c *Cluster) doJSON(method, addr, path string, body, out interface{}) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, addr+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("X-LinkHub-Token", c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s %s: %d %s", method, path, resp.StatusCode, string(b))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// GetJSON/PostJSON/DeleteJSON 面向 peer 节点的调用（proxy/automation 用）
func (c *Cluster) GetJSON(p *Node, path string, out interface{}) error {
	return c.doJSON("GET", p.Address, path, nil, out)
}

func (c *Cluster) PostJSON(p *Node, path string, body, out interface{}) error {
	return c.doJSON("POST", p.Address, path, body, out)
}

func (c *Cluster) DeleteJSON(p *Node, path string) error {
	return c.doJSON("DELETE", p.Address, path, nil, nil)
}

// ---------- 远程设备目录缓存（反熵） ----------
type RemoteConn struct {
	ID      int64                  `json:"id"`
	Kind    string                 `json:"kind"`
	Name    string                 `json:"name"`
	NodeID  string                 `json:"node_id"`
	Enabled bool                   `json:"enabled"`
	Params  map[string]interface{} `json:"params,omitempty"`
}

type RemoteDevice struct {
	models.Device
	Connections []RemoteConn `json:"connections"`
}

// DirectorySyncLoop 每 30s 拉一次在线 peer 的目录 → 远程缓存
func (c *Cluster) DirectorySyncLoop() {
	for {
		c.syncDirectoryOnce()
		time.Sleep(30 * time.Second)
	}
}

func (c *Cluster) syncDirectoryOnce() {
	for _, p := range c.ListNodes() {
		if p.IsSelf || p.Status != "online" {
			continue
		}
		var dir []RemoteDevice
		if err := c.doJSON("GET", p.Address, "/api/cluster/internal/directory", nil, &dir); err != nil {
			log.Printf("目录同步 %s 失败: %v", p.Name, err)
			continue
		}
		c.mu.Lock()
		if c.remoteDir == nil {
			c.remoteDir = map[string]map[int64]RemoteDevice{}
		}
		m := map[int64]RemoteDevice{}
		for _, d := range dir {
			d.NodeID = p.NodeID
			d.NodeName = p.Name
			d.NodeOnline = true
			m[d.ID] = d
		}
		c.remoteDir[p.NodeID] = m
		c.mu.Unlock()
	}
}

// RemoteDevices 合并后的远程设备（供 /api/devices 聚合）
func (c *Cluster) RemoteDevices() []RemoteDevice {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := []RemoteDevice{}
	for _, m := range c.remoteDir {
		for _, d := range m {
			out = append(out, d)
		}
	}
	return out
}

// RemoteDevice 取单个远程设备
func (c *Cluster) RemoteDevice(nodeID string, deviceID int64) *RemoteDevice {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if m, ok := c.remoteDir[nodeID]; ok {
		if d, ok := m[deviceID]; ok {
			return &d
		}
	}
	return nil
}

// ForgetNode 清掉该节点的远程目录缓存
func (c *Cluster) ForgetNode(nodeID string) {
	c.mu.Lock()
	delete(c.remoteDir, nodeID)
	c.mu.Unlock()
}

// ---------- 本机资源（串口） ----------
// ResourceWatchLoop 每 2s 快照本机串口，变化则更新 nodes.resources 并广播 resource_update
func (c *Cluster) ResourceWatchLoop(emit func(string, map[string]interface{})) {
	last := ""
	for {
		ports := connector.ListPorts()
		b, _ := json.Marshal(ports)
		if string(b) != last {
			last = string(b)
			res := map[string]interface{}{"serial_ports": json.RawMessage(b)}
			n := &models.Node{
				NodeID: c.selfID, Name: c.selfName, Address: c.address,
				Status: "online", IsSelf: true, LastSeen: time.Now(), Resources: res,
			}
			c.store.UpsertNode(n)
			if emit != nil {
				emit("resource_update", map[string]interface{}{
					"node_id": c.selfID, "serial_ports": json.RawMessage(b),
				})
			}
		}
		time.Sleep(2 * time.Second)
	}
}

// SelfResources 本机资源快照（internal/ping 用）
func (c *Cluster) SelfResources() map[string]interface{} {
	nodes, _ := c.store.ListNodes()
	for _, n := range nodes {
		if n.IsSelf {
			return n.Resources
		}
	}
	return map[string]interface{}{"serial_ports": serialPortsJSON()}
}

func serialPortsJSON() json.RawMessage {
	b, _ := json.Marshal(connector.ListPorts())
	return b
}
