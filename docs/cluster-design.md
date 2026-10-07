# 灵枢 LinkHub 集群模式设计方案

> 版本：v0.1
> 日期：2026-10-06
> 前提：单机版 P1 已交付（设备 CRUD + 串口会话 + Web 终端）

---

## 一、目标与原则

**目标**：多台运行 LinkHub 后端的机器组成集群，任何一台节点都能把自己本机的连接资源（串口、未来的网口/蓝牙）共享给整个集群；用户打开任意一个节点的 Web 界面，就能看到并使用所有节点上的设备。

**设计原则**：

1. **对称对等（Peer Mesh）**：无中心节点，每台后端代码完全相同，角色由数据归属决定——不引入主从，避免单点
2. **归属清晰**：设备/连接配置有唯一"归属节点"（home node）= 物理线缆所插的机器；归属节点权威存储，其余节点读副本缓存
3. **最小侵入**：单机模式是集群模式的特例（集群规模为 1）；不开集群时零额外开销
4. **局域网优先**：第一阶段面向实验室/机房同网段场景，节点可直连；跨网段中继放后期

## 二、总体架构

```
        ┌───────────────── 局域网（节点两两可达）─────────────────┐
        │                                                      │
  ┌─────┴──────┐          心跳/目录同步         ┌──────────────┴─┐
  │  节点 A     │ ◄──────────────────────────► │    节点 B       │
  │  LinkHub   │ ◄─────── HTTP + WS ─────────► │    LinkHub     │
  │  (本机DB)  │    X-LinkHub-Token 认证        │   (本机DB)      │
  └─────┬──────┘                                └───────┬────────┘
        │ /dev/ttyUSB0                                  │ /dev/ttyACM0
        ▼                                               ▼
    树莓派 4B                                        香橙派 5

  浏览器 ──► 任意节点 ──► 归属节点（控制面 RPC + 终端 WS 中继）
```

三种数据流，端口统一（复用 8000）：

| 流 | 协议 | 用途 |
|---|---|---|
| 控制面 | HTTP/JSON + `X-LinkHub-Token` | 目录同步、远程开/关会话、资源上报 |
| 数据面 | WebSocket 中继 | 浏览器↔接入节点↔归属节点 的终端字节流 |
| 发现面 | mDNS（可选）/ 静态配置 | 节点自动发现与加入 |

## 三、核心概念

### 3.1 节点（Node）

```yaml
node_id: "n-7f3a2b"        # 首次启动生成，持久化在 DB/配置文件，重启不变
name: "实验室主机A"          # 可改，展示用
address: "http://192.168.1.10:8000"   # 广播地址，其他节点据此回连
```

节点状态机：`joining → online ⇄ offline`（心跳超时 15s 判 offline，恢复自动 online）。

### 3.2 资源归属（Home Node）

- `ConnectionProfile` 增加 `node_id` 字段：物理端口所在节点。**串口永远绑定本机节点**（`/dev/ttyUSB0` 只在插着线的那台机器上有意义）
- `Device` 增加 `node_id`：设备的元数据归属。默认跟随其第一个连接配置的节点
- 归属节点**权威写**：任何节点收到"修改设备 X"的请求，若 X 不归本机，则内部转发（RPC）到归属节点执行，结果原路返回——对前端透明

### 3.3 设备目录（Directory Cache）

每个节点内存中维护全集群设备摘要缓存：

```
目录条目 = { node_id, device_id, name, tags, online, conn_kinds, updated_at }
```

- 来源：节点加入时全量拉取 + 之后靠事件增量更新（设备增删改 → 归属节点广播 `directory_update`）
- 兜底：60s 周期反熵（anti-entropy）对账，防事件丢失
- 归属节点离线 → 缓存条目标灰，列表里仍可看、不可操作

## 四、加入集群与发现

**方式一：手动加入（C1 先做，最可靠）**

```bash
# 在节点 B 上把 A 加为种子；握手时互换节点信息，之后自动互相发现其余节点（ gossip 式交换 peer 列表）
curl -X POST http://B:8000/api/cluster/join \
  -H 'Content-Type: application/json' \
  -d '{"address": "http://192.168.1.10:8000", "token": "<集群令牌>"}'
```

**方式二：mDNS 自动发现（C2）**

节点启动后广播 `_linkhub._tcp.local.`，同网段自动出现在"待加入节点"列表，管理员在界面上点"批准加入"（防止陌生节点擅自接入）。

**认证**：集群令牌共享密钥（`LINKHUB_CLUSTER_TOKEN`），节点间每个请求带 `X-LinkHub-Token`；加入握手成功后为每个 peer 签发独立会话密钥（HMAC-SHA256（集群令牌， nodeA+nodeB)），撤销单个节点不影响全局。

## 五、数据流详解

### 5.1 联邦设备列表（用户在任意节点看全部设备）

```
浏览器 → GET /api/devices (节点A)
         ├─ 本地库查询（本机设备）
         ├─ 读目录缓存（其他节点设备摘要）
         └─ 合并返回，每条带 node_id / node_name → 前端显示节点徽标
```

### 5.2 远程打开终端（核心链路）

设备 X 归属节点 B，用户在节点 A 的界面点"打开终端"：

```
1. 浏览器 → A: POST /api/connections/{X.conn}/open
2. A 发现 X.conn.node_id = B → 内部 RPC: POST B/api/cluster/internal/open {conn_id}
3. B 走正常 SessionManager.open()，返回 session_id（B 本地）
4. A 返回 { session_id, node_id: B } 给浏览器
5. 浏览器 → A: WS /ws/terminal/{session_id}?node=B
6. A 开中继：自己 ↔ WS B/ws/cluster/relay/{session_id}，双向管道拷贝字节流
7. B 侧就是本地终端会话，日志/遮蔽/重连全部复用单机逻辑
```

关键点：**归属节点无感知被中继**——relay 端点复用现有 terminal WS 处理函数；接入节点只做字节转发 + 限流。终端延迟 = 浏览器→A + A→B 各一跳，局域网内可忽略。

### 5.3 串口资源聚合

`GET /api/serial/ports` 在集群模式下变为：

```json
[
  { "node": "本机",        "device": "/dev/ttyUSB0", "description": "CH340" },
  { "node": "实验室主机B",  "device": "/dev/ttyACM0", "description": "Gadget" }
]
```

前端创建连接配置时选了远程节点的端口 → `node_id` 记为 B，打开会话时自动走路由转发。USB 热插拔事件由归属节点广播，各节点目录缓存同步刷新下拉列表。

### 5.4 事件转发

节点 B 的 `session_status` 事件 → 除本地事件总线外，同步推给所有 peer（WS 长连接 `/ws/cluster/events`）→ 各节点注入本地总线 → 前端徽标全集群实时一致。

## 六、数据模型与 API 变更

### 6.1 模型增量

```python
class Node(Base):           # 新表
    node_id: str  (pk)      # n-xxxxxx
    name: str
    address: str            # http://ip:port
    status: str             # online / offline
    is_self: bool
    last_seen: datetime
    resources: dict         # {serial_ports: [...], connector_kinds: [...]}

class Device:               # 加字段
    node_id: str (默认本机)

class ConnectionProfile:    # 加字段
    node_id: str (默认本机)
```

集群内全局标识 = `(node_id, local_id)`；本地整数 ID 保持不变，单机行为零影响。

### 6.2 新增 API

```
# 管理面（前端用）
GET    /api/cluster/nodes                    # 节点列表+状态+资源
POST   /api/cluster/join                     # {address, token} 加入集群
POST   /api/cluster/leave
GET    /api/cluster/nodes/{id}/resources     # 单节点资源详情

# 节点间内部面（X-LinkHub-Token 认证，不暴露给前端）
POST   /api/cluster/internal/handshake       # 加入握手：互换 node 信息+peer 列表
GET    /api/cluster/internal/directory       # 全量设备目录
POST   /api/cluster/internal/open            # 远程开会话
POST   /api/cluster/internal/close
WS     /ws/cluster/relay/{session_id}        # 终端字节中继
WS     /ws/cluster/events                    # 事件订阅（peer 间）
GET    /api/cluster/internal/ping            # 心跳
```

## 七、故障与边界

| 场景 | 行为 |
|---|---|
| 归属节点离线 | 其设备在列表标灰；打开会话返回 503 + "节点离线"；缓存保留 |
| 会话中归属节点掉线 | 中继 WS 断开 → 前端提示；节点恢复后可重新打开（串口侧自动重连仍由归属节点负责） |
| 网络分区 | 各分区独立可用（本地设备正常操作）；恢复后反熵对账合并目录 |
| 两台节点同名设备 | 允许：全局标识含 node_id，前端显示节点前缀区分 |
| 令牌错误 | 握手拒绝 403，记审计日志 |
| 节点被移除 | 广播 revoke，各节点清理其目录缓存与 peer 密钥 |

## 八、配置

```bash
# .env / 环境变量
LINKHUB_CLUSTER_ENABLED=true
LINKHUB_CLUSTER_TOKEN=<openssl rand -hex 32>     # 集群令牌，所有节点一致
LINKHUB_NODE_NAME=实验室主机A
LINKHUB_ADVERTISE_ADDR=http://192.168.1.10:8000  # 广播地址（其他节点能访问到的地址）
LINKHUB_CLUSTER_MDNS=true                        # C2 阶段
```

`LINKHUB_CLUSTER_ENABLED=false`（默认）即单机模式，行为与现状完全一致。

## 九、前端变更

1. **设备列表**：新增"节点"列（本机=蓝色徽标，远程=节点名徽标）；支持按节点筛选
2. **连接配置表单**：串口下拉的分组按节点分组（"本机 / 实验室主机B / …"）
3. **仪表盘**：新增集群卡片（节点数、在线节点、各节点资源数）
4. **集群管理页**（新）：节点列表、加入集群表单、待批准节点（mDNS 发现）、移除节点
5. **终端页**：标题栏显示 `设备名 @ 节点B`；中继断开时提示归属节点状态

## 十、分期实施

| 阶段 | 内容 | 工作量估计 |
|---|---|---|
| **C1** | Node 表 + 令牌认证 + 手动 join + 联邦设备列表 + 远程开关会话 + **WS 终端中继** | 2~3 天，集群可用闭环 |
| C2 | mDNS 自动发现 + 资源热插拔广播 + 集群管理页 | 1~2 天 |
| C3 | 目录反熵对账 + 离线标灰 + 跨节点批量命令 | 2 天 |
| C4 | 跨网段反向隧道（agent 主动外拨中继）+ 节点级 RBAC | 视需求 |

C1 验收标准：两台机器各插一块开发板，在 A 的界面上能打开 B 所插板子的终端并正常登录执行命令；拔掉 B 的网线，A 上该设备标灰；插回后自动恢复。

## 十一、为什么不选另外两种拓扑

- **中心+Agent（hub-spoke）**：中心宕机全集群瘫痪；agent 需要裁剪版代码——违背"任何一台后端都能共享"的对等诉求
- **全量复制（每节点存全量 DB）**：写冲突需要 CRDT/向量时钟，对"设备台账"这种低频写场景属于过度设计；目录缓存 + 归属权威已足够
