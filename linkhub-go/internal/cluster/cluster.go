package cluster

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"linkhub/internal/models"
	"linkhub/internal/store"
)

type Node struct {
	models.Node
	LastPing time.Time
}

type Cluster struct {
	mu       sync.RWMutex
	selfID   string
	selfName string
	address  string
	token    string
	peers    map[string]*Node
	store    *store.Store
	client   *http.Client
	onEvent  func(event string, data map[string]interface{})
}

func NewCluster(st *store.Store, selfID, selfName, address, token string) *Cluster {
	return &Cluster{
		selfID: selfID, selfName: selfName, address: address, token: token,
		peers: map[string]*Node{}, store: st,
		client: &http.Client{Timeout: 6 * time.Second},
	}
}

func (c *Cluster) SelfID() string { return c.selfID }

// ---------- 握手 ----------
func (c *Cluster) Handshake(address, name string, remoteNodeID string) (*Node, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := &Node{Node: models.Node{
		NodeID: remoteNodeID, Name: name, Address: address, Status: "online", LastSeen: time.Now(),
	}}
	c.peers[remoteNodeID] = n
	c.store.UpsertNode(&n.Node)
	return n, nil
}

func (c *Cluster) Join(address string) (*Node, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"node_id": c.selfID, "name": c.selfName, "address": c.address,
	})
	req, _ := http.NewRequest("POST", address+"/api/cluster/internal/handshake", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-LinkHub-Token", c.token)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("无法连接 %s: %w", address, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 403 {
		return nil, fmt.Errorf("握手被拒绝：令牌不匹配")
	}
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("握手失败 %d: %s", resp.StatusCode, string(b))
	}
	var result struct {
		NodeID  string `json:"node_id"`
		Name    string `json:"name"`
		Address string `json:"address"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	n := &Node{Node: models.Node{
		NodeID: result.NodeID, Name: result.Name, Address: result.Address,
		Status: "online", LastSeen: time.Now(),
	}}
	c.mu.Lock()
	c.peers[result.NodeID] = n
	c.mu.Unlock()
	c.store.UpsertNode(&n.Node)
	// 启动心跳
	go c.heartbeatLoop(n)
	return n, nil
}

// ---------- 心跳 ----------
func (c *Cluster) heartbeatLoop(n *Node) {
	for {
		time.Sleep(5 * time.Second)
		if err := c.ping(n); err != nil {
			c.setStatus(n, "offline")
		} else {
			if n.Status != "online" {
				c.setStatus(n, "online")
			}
			n.LastPing = time.Now()
		}
	}
}

func (c *Cluster) ping(n *Node) error {
	req, _ := http.NewRequest("GET", n.Address+"/api/cluster/internal/ping", nil)
	req.Header.Set("X-LinkHub-Token", c.token)
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func (c *Cluster) setStatus(n *Node, status string) {
	c.mu.Lock()
	n.Status = status
	c.mu.Unlock()
	n.LastSeen = time.Now()
	c.store.UpsertNode(&n.Node)
	if c.onEvent != nil {
		c.onEvent("node_status", map[string]interface{}{
			"node_id": n.NodeID, "name": n.Name, "status": status,
		})
	}
}

func (c *Cluster) OnEvent(f func(string, map[string]interface{})) { c.onEvent = f }

// ---------- 节点列表 ----------
func (c *Cluster) ListNodes() []models.Node {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := []models.Node{{
		NodeID: c.selfID, Name: c.selfName, Address: c.address,
		Status: "online", IsSelf: true, LastSeen: time.Now(),
	}}
	for _, p := range c.peers {
		out = append(out, p.Node)
	}
	return out
}

func (c *Cluster) GetPeer(nodeID string) *Node {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.peers[nodeID]
}

func (c *Cluster) RemovePeer(nodeID string) {
	c.mu.Lock()
	delete(c.peers, nodeID)
	c.mu.Unlock()
	c.store.DeleteNode(nodeID)
}

// ---------- 定向发现（LINKHUB_CLUSTER_PEERS） ----------
func (c *Cluster) DirectPeersLoop(peers []string) {
	for {
		time.Sleep(5 * time.Second)
		for _, addr := range peers {
			if c.GetPeer(addr) != nil {
				continue
			}
			if _, err := c.Join(addr); err != nil {
				log.Printf("集群定向加入 %s 失败: %v", addr, err)
			}
		}
	}
}

// ---------- UDP 广播发现 ----------
func (c *Cluster) BeaconLoop(port int, ifaceBroadcasts []string) {
	card, _ := json.Marshal(map[string]string{
		"v": "1", "node_id": c.selfID, "name": c.selfName, "address": c.address,
	})
	for _, brd := range ifaceBroadcasts {
		go c.beaconSend(port, brd, card)
	}
	// 接收
	addr := &net.UDPAddr{IP: net.IPv4zero, Port: port}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return
	}
	buf := make([]byte, 1024)
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		var card struct {
			NodeID  string `json:"node_id"`
			Name    string `json:"name"`
			Address string `json:"address"`
		}
		if json.Unmarshal(buf[:n], &card) != nil || card.NodeID == c.selfID {
			continue
		}
		if c.GetPeer(card.NodeID) == nil {
			// 新节点 → 待批准（事件通知前端）
			if c.onEvent != nil {
				c.onEvent("node_discovered", map[string]interface{}{
					"node_id": card.NodeID, "name": card.Name, "address": card.Address,
				})
			}
		}
	}
}

func (c *Cluster) beaconSend(port int, broadcastAddr string, card []byte) {
	addr := &net.UDPAddr{IP: net.ParseIP(broadcastAddr), Port: port}
	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return
	}
	defer conn.Close()
	for {
		conn.Write(card)
		time.Sleep(3 * time.Second)
	}
}

func (c *Cluster) Token() string    { return c.token }
func (c *Cluster) SelfName() string { return c.selfName }
func (c *Cluster) Address() string  { return c.address }
