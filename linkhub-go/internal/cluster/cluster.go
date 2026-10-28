package cluster

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
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
	desktopPort int                          // 本机 VNC 端口（桌面共享能力探测用）
	peers    map[string]*Node
	pending  map[string]Pending                 // UDP 发现的待加入节点
	known    map[string]KnownNode               // gossip 学到的间接节点（跨网段经 via 中转）
	seedNodes []models.Node                     // 上次运行遗留的节点（启动即展示，重连后转在线）
	remoteDir map[string]map[int64]RemoteDevice // nodeID → deviceID → 远程设备缓存
	store    *store.Store
	client   *http.Client
	onEvent  func(event string, data map[string]interface{})
}

func NewCluster(st *store.Store, selfID, selfName, address, token string) *Cluster {
	c := &Cluster{
		selfID: selfID, selfName: selfName, address: address, token: token,
		peers: map[string]*Node{}, pending: map[string]Pending{},
		known: map[string]KnownNode{}, remoteDir: map[string]map[int64]RemoteDevice{},
		store: st,
		client: &http.Client{Timeout: 6 * time.Second},
	}
	// 上次运行加入过的节点从库加载：一律先以离线态展示（库里残留的 online 不可信），
	// DirectPeersLoop/持久化地址自动重连成功后才转在线
	if nodes, err := st.ListNodes(); err == nil {
		for _, n := range nodes {
			if n.NodeID != selfID {
				n.Status = "offline"
				c.seedNodes = append(c.seedNodes, n)
			}
		}
	}
	return c
}

// ---------- 已加入节点的持久化（重启后自动重连） ----------
const joinedAddrsKey = "joined_addrs"

func (c *Cluster) joinedAddrList() []string {
	list := []string{}
	if v := c.store.GetMeta(joinedAddrsKey); v != "" {
		json.Unmarshal([]byte(v), &list)
	}
	return list
}

func (c *Cluster) addJoinedAddr(addr string) {
	list := c.joinedAddrList()
	for _, a := range list {
		if a == addr {
			return
		}
	}
	list = append(list, addr)
	b, _ := json.Marshal(list)
	c.store.SetMeta(joinedAddrsKey, string(b))
}

func (c *Cluster) removeJoinedAddr(addr string) {
	list := c.joinedAddrList()
	out := list[:0]
	for _, a := range list {
		if a != addr {
			out = append(out, a)
		}
	}
	if len(out) == len(list) {
		return
	}
	b, _ := json.Marshal(out)
	c.store.SetMeta(joinedAddrsKey, string(b))
}

func (c *Cluster) SelfID() string { return c.selfID }

// SetDesktopPort 设置本机 VNC 端口（main 注入，能力探测/广播用）
func (c *Cluster) SetDesktopPort(port int) { c.desktopPort = port }

// ---------- 握手 ----------
func (c *Cluster) Handshake(address, name string, remoteNodeID string) (*Node, error) {
	if remoteNodeID == c.selfID {
		return nil, fmt.Errorf("不能将本机加入集群")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// 同地址换 ID（对端重装/换数据目录）：清掉旧身份在 peers/known/seed 的残留
	c.dropByAddressLocked(address, remoteNodeID)
	n := &Node{Node: models.Node{
		NodeID: remoteNodeID, Name: name, Address: address, Status: "online", LastSeen: time.Now(),
	}}
	c.peers[remoteNodeID] = n
	c.store.UpsertNode(&n.Node)
	return n, nil
}

// dropByAddressLocked 删除指定地址下所有非 keepID 的节点条目（需持 c.mu）
func (c *Cluster) dropByAddressLocked(address, keepID string) {
	for id, p := range c.peers {
		if p.Address == address && id != keepID {
			delete(c.peers, id)
			c.store.DeleteNode(id)
		}
	}
	for id, k := range c.known {
		if k.Node.Address == address && id != keepID {
			delete(c.known, id)
		}
	}
	seed := c.seedNodes[:0]
	for _, sn := range c.seedNodes {
		if sn.Address == address && sn.NodeID != keepID {
			c.store.DeleteNode(sn.NodeID)
			continue
		}
		seed = append(seed, sn)
	}
	c.seedNodes = seed
}

func (c *Cluster) Join(address string) (*Node, error) {
	if address == c.address {
		return nil, fmt.Errorf("不能加入本机节点")
	}
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
	// 自加入兜底：地址写法不同（127.0.0.1 vs LAN IP）时靠握手返回的 node_id 识别
	if result.NodeID == c.selfID {
		return nil, fmt.Errorf("不能加入本机节点")
	}
	n := &Node{Node: models.Node{
		NodeID: result.NodeID, Name: result.Name, Address: result.Address,
		Status: "online", LastSeen: time.Now(),
	}}
	c.mu.Lock()
	c.peers[result.NodeID] = n
	c.mu.Unlock()
	c.store.UpsertNode(&n.Node)
	c.noteJoined(result.NodeID)
	c.addJoinedAddr(address)
	seed := c.seedNodes[:0]
	for _, sn := range c.seedNodes {
		if sn.NodeID != result.NodeID {
			seed = append(seed, sn)
		}
	}
	c.seedNodes = seed
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
		// 本机地址（自引用/旧身份）与"同地址但 ID 不同的直连 peer"（旧身份残留）一律不收
		if p.Address == c.address || c.peerAddrMismatch(p.Address, p.NodeID) {
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

// peerAddrMismatch 同地址已有直连 peer 但 node_id 不同（旧身份残留）
func (c *Cluster) peerAddrMismatch(address, nodeID string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, p := range c.peers {
		if p.Address == address && p.NodeID != nodeID {
			return true
		}
	}
	return false
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
		// 该 peer 已被替换/移除（身份变更等）→ 旧心跳循环退出
		if c.GetPeer(n.NodeID) != n {
			return
		}
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
	// 心跳顺带 gossip：对方的节点列表（含状态）刷新间接节点表；
	// 同时存对端资源快照（串口/桌面能力），节点列表据此展示
	var result struct {
		NodeID    string                 `json:"node_id"`
		Peers     []models.Node          `json:"peers"`
		Resources map[string]interface{} `json:"resources"`
	}
	if json.NewDecoder(resp.Body).Decode(&result) == nil {
		// 身份校验：同地址应答的 node_id 变了 = 对端重装/换数据目录，
		// 旧 ID 是残留身份——清掉并按新 ID 重新登记（旧心跳循环随之退出）
		if result.NodeID != "" && result.NodeID != n.NodeID {
			log.Printf("节点 %s 身份变更: %s → %s，清理旧条目", n.Address, n.NodeID, result.NodeID)
			oldID := n.NodeID
			c.mu.Lock()
			delete(c.peers, oldID)
			c.mu.Unlock()
			c.store.DeleteNode(oldID)
			if nn, err := c.Handshake(n.Address, n.Name, result.NodeID); err == nil {
				go c.heartbeatLoop(nn)
				go c.syncDirectoryOnce()
			}
			return fmt.Errorf("peer identity changed: %s → %s", oldID, result.NodeID)
		}
		if result.Resources != nil {
			c.mu.Lock()
			n.Resources = result.Resources
			c.mu.Unlock()
		}
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
// 按地址去重（self > 直连 peer > gossip > seed）：同地址多身份（重装换 ID）只保留最权威一条
func (c *Cluster) ListNodes() []models.Node {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := []models.Node{{
		NodeID: c.selfID, Name: c.selfName, Address: c.address,
		Status: "online", IsSelf: true, LastSeen: time.Now(),
		Resources: c.SelfResources(), // 本机资源实时计算（探针有缓存），保证节点列表可见桌面能力
	}}
	seen := map[string]bool{c.address: true, "": true}
	for _, p := range c.peers {
		if p.NodeID == c.selfID || seen[p.Address] { // 自引用残留与地址重复都剔除
			continue
		}
		seen[p.Address] = true
		out = append(out, p.Node)
	}
	for _, k := range c.known {
		if seen[k.Node.Address] {
			continue
		}
		seen[k.Node.Address] = true
		out = append(out, k.Node) // 间接节点（带 via）
	}
	for _, sn := range c.seedNodes { // 上次运行的节点：重连成功前以离线态显示
		if sn.NodeID == c.selfID || seen[sn.Address] {
			continue
		}
		seen[sn.Address] = true
		out = append(out, sn)
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
	n := c.peers[nodeID]
	delete(c.peers, nodeID)
	delete(c.known, nodeID)
	seed := c.seedNodes[:0]
	for _, sn := range c.seedNodes {
		if sn.NodeID != nodeID {
			seed = append(seed, sn)
		}
	}
	c.seedNodes = seed
	c.mu.Unlock()
	if n != nil {
		c.removeJoinedAddr(n.Address) // 主动移除后不再自动重连
	}
	c.store.DeleteNode(nodeID)
}

// ---------- 定向发现（LINKHUB_CLUSTER_PEERS） ----------
func (c *Cluster) DirectPeersLoop(peers []string) {
	for {
		time.Sleep(5 * time.Second)
		addrs := append([]string{}, peers...)
		for _, a := range c.joinedAddrList() {
			dup := false
			for _, x := range addrs {
				if x == a {
					dup = true
					break
				}
			}
			if !dup {
				addrs = append(addrs, a)
			}
		}
		// 本机地址跳过（历史版本可能把自己写进了 joined_addrs）
		for _, addr := range addrs {
			if addr == c.address {
				continue
			}
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
// seeds 静态种子节点：跨网段广播不可达时按 UDP 单播发卡片，收到卡片的对端会单播回包，实现双向发现
func (c *Cluster) BeaconLoop(port int, broadcasts func() []string, seeds []string) {
	card, _ := json.Marshal(map[string]string{
		"v": "1", "node_id": c.selfID, "name": c.selfName, "address": c.address,
		"dport": strconv.Itoa(port), // 对端回包用：发送源端口是临时端口，无法作为回包目标
	})
	// 发送：广播 + 种子单播
	go func() {
		for {
			for _, brd := range broadcasts() {
				c.beaconSendCard(port, brd, card)
			}
			for _, seed := range seeds {
				host, sport, err := net.SplitHostPort(seed)
				if err != nil {
					host, sport = seed, strconv.Itoa(port)
				}
				if p, err := strconv.Atoi(sport); err == nil {
					c.beaconSendCard(p, host, card)
				}
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
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		var card struct {
			NodeID  string `json:"node_id"`
			Name    string `json:"name"`
			Address string `json:"address"`
			Dport   string `json:"dport"`
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
			// 单播回包：跨网段时广播不可达，让对端也能发现我（回包走单播，无广播风暴）
			reply, _ := json.Marshal(map[string]string{
				"v": "1", "node_id": c.selfID, "name": c.selfName, "address": c.address,
			})
			dport, err := strconv.Atoi(card.Dport)
			if err != nil || dport <= 0 {
				dport = port
			}
			go c.beaconSendCard(dport, src.IP.String(), reply)
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
