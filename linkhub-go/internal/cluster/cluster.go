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
	pending  map[string]Pending                 // UDP 发现的待加入节点
	known    map[string]KnownNode               // gossip 学到的间接节点（跨网段经 via 中转）
	remoteDir map[string]map[int64]RemoteDevice // nodeID → deviceID → 远程设备缓存
	store    *store.Store
	client   *http.Client
	onEvent  func(event string, data map[string]interface{})
}

func NewCluster(st *store.Store, selfID, selfName, address, token string) *Cluster {
	return &Cluster{
		selfID: selfID, selfName: selfName, address: address, token: token,
		peers: map[string]*Node{}, pending: map[string]Pending{},
		known: map[string]KnownNode{}, remoteDir: map[string]map[int64]RemoteDevice{},
		store: st,
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
		NodeID  string        `json:"node_id"`
		Name    string        `json:"name"`
		Address string        `json:"address"`
		Peers   []models.Node `json:"peers"`
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
	c.noteJoined(result.NodeID)
	c.mu.Lock()
	delete(c.known, result.NodeID) // 已直连，不再是间接节点
	c.mu.Unlock()
	// gossip：对方把自己认识的节点带回来了 → 记为间接节点（经对方中转可达），
	// 目录/开会话/终端流都能经此桥接，无需直连
	c.gossipFrom(result.Peers, result.NodeID)
	// 启动心跳
	go c.heartbeatLoop(n)
	return n, nil
}

// gossipFrom 把 handshake/ping 带回的节点列表记为间接节点（via=来源节点）
func (c *Cluster) gossipFrom(peers []models.Node, via string) {
	for _, p := range peers {
		if p.NodeID == "" || p.Address == "" || p.NodeID == c.selfID {
			continue
		}
		if c.GetPeer(p.NodeID) != nil || c.IsDismissed(p.NodeID) {
			continue
		}
		if p.Status == "" {
			p.Status = "online"
		}
		c.mu.Lock()
		prev, existed := c.known[p.NodeID]
		p.Via = via
		c.known[p.NodeID] = KnownNode{Node: p}
		c.mu.Unlock()
		if !existed || prev.Node.Status != p.Status {
			if c.onEvent != nil {
				c.onEvent("node_status", map[string]interface{}{
					"node_id": p.NodeID, "name": p.Name, "status": p.Status, "via": via,
				})
			}
		}
	}
}

// KnownNode 经 gossip 学到的间接节点（跨网段经 via 中转）
type KnownNode struct {
	Node models.Node
}

// ResolveRoute 解析目标节点的下一跳：直连 peer 直接返回；间接节点返回其 via 直连 peer
// 返回 (下一跳 peer, 目标是否间接, 是否可达)
func (c *Cluster) ResolveRoute(nodeID string) (*Node, bool, bool) {
	if p := c.GetPeer(nodeID); p != nil {
		return p, false, true
	}
	c.mu.RLock()
	k, ok := c.known[nodeID]
	c.mu.RUnlock()
	if !ok {
		return nil, false, false
	}
	if p := c.GetPeer(k.Node.Via); p != nil && p.Status == "online" {
		return p, true, true
	}
	return nil, true, false
}

// ---------- 心跳 ----------
func (c *Cluster) heartbeatLoop(n *Node) {
	for {
		time.Sleep(5 * time.Second)
		if err := c.ping(n); err != nil {
			c.setStatus(n, "offline")
			// 经该节点中转的间接节点一并标记离线
			c.mu.Lock()
			for id, k := range c.known {
				if k.Node.Via == n.NodeID && k.Node.Status != "offline" {
					k.Node.Status = "offline"
					c.known[id] = k
				}
			}
			c.mu.Unlock()
		} else {
			if n.Status != "online" {
				c.setStatus(n, "online")
				go c.syncDirectoryOnce() // 上线即拉目录
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
	// 心跳顺带 gossip：对方的节点列表（含状态）刷新间接节点表
	var result struct {
		Peers []models.Node `json:"peers"`
	}
	if json.NewDecoder(resp.Body).Decode(&result) == nil {
		c.gossipFrom(result.Peers, n.NodeID)
	}
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
	for _, k := range c.known {
		out = append(out, k.Node) // 间接节点（带 via）
	}
	return out
}

func (c *Cluster) GetPeer(nodeID string) *Node {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.peers[nodeID]
}

// GetPeerByAddress 按地址找 peer（DirectPeersLoop 用，避免误判导致重复 Join）
func (c *Cluster) GetPeerByAddress(addr string) *Node {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, p := range c.peers {
		if p.Address == addr {
			return p
		}
	}
	return nil
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
			if c.GetPeerByAddress(addr) != nil {
				continue
			}
			if _, err := c.Join(addr); err != nil {
				log.Printf("集群定向加入 %s 失败: %v", addr, err)
			}
		}
	}
}

// ---------- UDP 广播发现 ----------
// BeaconLoop 每 3s 向 broadcasts() 返回的广播地址发节点卡片，并监听对端卡片。
// broadcasts 动态读取（设置页保存网卡后下一轮生效）
func (c *Cluster) BeaconLoop(port int, broadcasts func() []string) {
	card, _ := json.Marshal(map[string]string{
		"v": "1", "node_id": c.selfID, "name": c.selfName, "address": c.address,
	})
	// 发送
	go func() {
		for {
			for _, brd := range broadcasts() {
				c.beaconSendCard(port, brd, card)
			}
			time.Sleep(3 * time.Second)
		}
	}()
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
		if c.GetPeer(card.NodeID) == nil && !c.IsDismissed(card.NodeID) && !c.isKnown(card.NodeID) {
			// 新节点 → 待加入列表
			c.AddPending(card.NodeID, card.Name, card.Address)
			if c.onEvent != nil {
				c.onEvent("node_discovered", map[string]interface{}{
					"node_id": card.NodeID, "name": card.Name, "address": card.Address,
				})
			}
		}
	}
}

// beaconSendCard 单次向广播地址发一张卡片（由 BeaconLoop 周期调用）
func (c *Cluster) beaconSendCard(port int, broadcastAddr string, card []byte) {
	addr := &net.UDPAddr{IP: net.ParseIP(broadcastAddr), Port: port}
	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return
	}
	defer conn.Close()
	conn.Write(card)
}

func (c *Cluster) Token() string    { return c.token }
func (c *Cluster) SelfName() string { return c.selfName }
func (c *Cluster) Address() string  { return c.address }
