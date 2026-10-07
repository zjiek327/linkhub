# 设备连接平台 — 需求梳理与实现方案

> 版本：v0.1（第一阶段规划）
> 日期：2026-10-06

---

## 一、需求梳理

### 1.1 项目愿景

打造一个**统一的设备连接与管理平台**：后端通过插件化的"连接器"适配一切设备接入方式（串口/USB、网络 SSH/Telnet、蓝牙 BLE/经典蓝牙、Modbus、MQTT、自定义协议……），前端提供统一的设备台账、配置管理、在线终端与批量操作能力。

一句话：**一个平台，连接所有设备。**

### 1.2 用户角色与典型场景

| 角色 | 典型场景 |
|---|---|
| 嵌入式/硬件工程师 | 新板子到手，用平台串口连上树莓派，打开 Web 终端装系统配置 |
| 运维/实验室管理员 | 管理机柜/实验室里几十块开发板，按分组查看在线状态，批量下发命令 |
| 测试工程师 | 保存每款设备的配置模板，新设备一键套用，自动执行冒烟脚本 |
|  IoT 开发者 | 同一面板管理 BLE 传感器、MQTT 网关、串口调试板 |

### 1.3 功能需求（FR）

**核心域：设备管理**
- FR-1 设备 CRUD：名称、分组、标签、描述、照片、位置、负责人
- FR-2 设备与"连接配置"解耦：一台设备可挂多个连接配置（如树莓派同时有串口 + SSH + BLE）
- FR-3 设备配置模板库：内置 + 自定义模板（树莓派、香蕉派、香橙派、ESP32……），创建设备时一键套用
- FR-4 连接配置 CRUD：连接方式（serial/ssh/bluetooth/network…）+ 参数字典（波特率、IP、凭证引用等）
- FR-5 凭证管理：密码/密钥加密存储，配置中只存引用

**核心域：连接与通信**
- FR-6 打开/关闭连接会话，实时上报会话状态（connecting / online / offline / error）
- FR-7 Web 终端：浏览器内全双工终端，对接会话收发数据
- FR-8 指令下发：单设备发命令、按分组/标签批量下发
- FR-9 会话日志：收发数据落盘，可回放、可导出
- FR-10 串口自动发现：枚举本机可用串口（/dev/ttyUSB*、/dev/ttyACM*、COM*），支持热插拔事件

**平台能力**
- FR-11 连接器插件体系：新连接方式以插件接入，不改核心代码
- FR-12 用户与权限（P2 阶段）：RBAC，设备粒度授权
- FR-13 审计日志：谁在什么时候对哪台设备做了什么

### 1.4 非功能需求（NFR）

- **可扩展性**：连接器 = 插件，协议无关的统一抽象（open / close / read / write / status）
- **实时性**：终端数据延迟 < 200ms，WebSocket 推送
- **稳定性**：串口断线自动检测与重连策略；会话崩溃不影响平台
- **安全性**：凭证加密（AES-GCM）；API 鉴权（JWT）；Web 终端输入有权限校验
- **可部署性**：单机 Docker Compose 一键拉起；资源占用低（后端可跑在树莓派上自管理）
- **国际化**：中/英文界面（P2）

### 1.5 阶段划分

| 阶段 | 目标 | 连接方式 |
|---|---|---|
| **P1（本次）** | 跑通"设备管理 + 串口连接 + Web 终端"闭环 | 串口/USB → 树莓派 4B |
| P2 | 网络连接 + 批量操作 + 权限 | SSH/Telnet、ping 探测 |
| P3 | 无线与 IoT | BLE、经典蓝牙、MQTT 网关 |
| P4 | 自动化 | 脚本编排、定时任务、固件/文件分发 |
| P5 | 生态 | 模板市场、第三方插件 API、移动端 |

---

## 二、总体架构

### 2.1 架构图

```
┌────────────────────────────────────────────────────────┐
│                    前端 (Vue3 SPA)                      │
│  设备台账 │ 模板库 │ Web终端(xterm.js) │ 批量操作 │ 日志 │
└───────────────▲───────────────────▲────────────────────┘
        REST/JSON │                   │ WebSocket(终端/事件)
┌───────────────┴───────────────────┴────────────────────┐
│                    后端 (FastAPI)                       │
│ ┌──────────┐ ┌───────────┐ ┌────────────┐ ┌─────────┐ │
│ │ 设备服务  │ │ 模板服务   │ │ 会话管理器  │ │ 事件总线 │ │
│ └──────────┘ └───────────┘ └─────┬──────┘ └─────────┘ │
│ ┌──────────┐ ┌───────────┐       │          ┌─────────┐ │
│ │ 凭证 vault│ │ 审计/日志  │       │          │ 任务队列 │ │
│ └──────────┘ └───────────┘       ▼          └─────────┘ │
│                       ┌──────────────────────┐         │
│                       │  连接器插件层(Connector)│         │
│                       │ Serial│SSH│BLE│MQTT│… │         │
│                       └──────────────────────┘         │
└────────────────────────────┬───────────────────────────┘
                             │
        ┌────────────┬───────┴───────┬────────────┐
        ▼            ▼               ▼            ▼
   /dev/ttyUSB0   SSH:22         BLE GATT     MQTT Broker
   树莓派4B(GPIO)  开发板/服务器    传感器/手环    IoT 设备群
```

### 2.2 后端模块划分

| 模块 | 职责 |
|---|---|
| `device` | 设备、分组、标签的 CRUD 与查询 |
| `template` | 设备配置模板（内置 YAML + 用户自定义） |
| `connection` | 连接配置 CRUD，与设备多对一关联 |
| `connector` | 连接器插件框架：接口定义、注册表、生命周期 |
| `session` | 会话管理器：打开/关闭/重连/心跳，一个活跃连接 = 一个 Session |
| `terminal` | WebSocket 网关：前端 xterm.js ↔ Session 双向流 |
| `credential` | 凭证加密存取（AES-GCM，主密钥来自环境变量） |
| `audit` | 操作审计与会话日志落盘 |
| `discovery` | 串口/网络/蓝牙设备自动发现（P1 只做串口枚举） |

### 2.3 连接器插件体系（核心抽象）

```python
class Connector(Protocol):
    kind: str  # "serial" | "ssh" | "ble" | ...

    async def open(self, profile: ConnectionProfile) -> None: ...
    async def close(self) -> None: ...
    async def write(self, data: bytes) -> None: ...
    async def read(self) -> AsyncIterator[bytes]: ...   # 数据流
    async def health(self) -> ConnStatus: ...

    @classmethod
    def schema(cls) -> dict: ...   # 参数 JSON Schema，前端据此自动渲染表单
```

要点：
- 每种连接方式实现同一接口，**新增协议 = 新增一个插件文件**，框架自动注册
- `schema()` 返回参数 JSON Schema → 前端表单自动生成，前后端零耦合加协议
- Session 层做断线重连、背压、超时，与具体协议无关

### 2.4 前端页面（P1）

1. **仪表盘**：设备总数、在线数、活跃会话
2. **设备列表**：表格 + 分组树 + 标签筛选 + 在线状态徽标
3. **设备详情**：基本信息、连接配置列表、"打开终端"按钮、会话日志
4. **模板库**：内置模板卡片，可"用模板创建设备"
5. **Web 终端**：xterm.js 全屏终端，支持多 Tab（多会话）

---

## 三、技术选型

| 层 | 选型 | 理由 |
|---|---|---|
| 后端框架 | **Python 3.12 + FastAPI** | 异步 IO 适合大量并发连接；硬件库生态最全 |
| 串口 | **pyserial + pyserial-asyncio** | 事实标准，支持热插拔枚举 |
| SSH（P2） | asyncssh / paramiko | 异步优先 asyncssh |
| BLE（P3） | bleak | 跨平台异步 BLE |
| 数据库 | **SQLite**（起步）→ PostgreSQL（规模化） | 单机零依赖，ORM 层不锁死 |
| ORM | SQLAlchemy 2.0 + Alembic | 迁移规范 |
| 实时通道 | FastAPI WebSocket | 终端流 + 事件推送 |
| 前端 | **Vue 3 + Vite + TypeScript + Element Plus** | 后台管理系统快 |
| 终端组件 | **xterm.js**（+ fit / web-links 插件） | Web 终端事实标准 |
| 部署 | Docker Compose（backend / frontend / nginx） | 一键拉起 |

---

## 四、数据模型（核心表）

```
devices          设备
  id, name, group_id, description, location, owner,
  template_id(可空), tags[], status, created_at, updated_at

device_groups    分组（树形）
  id, name, parent_id

templates        设备配置模板
  id, key(raspberry_pi_4b), name, category, icon,
  description, default_connections(jsonb), spec(jsonb), builtin(bool)

connection_profiles   连接配置（设备 : 配置 = 1 : N）
  id, device_id, kind(serial|ssh|ble|…), name,
  params(jsonb)          -- {port, baudrate, bytesize, parity, stopbits}
  credential_id(可空), enabled, created_at

credentials      凭证（密文）
  id, name, type(password|key), secret_enc, created_at

sessions         会话（运行态，也落盘留痕）
  id, connection_id, opened_by, opened_at, closed_at,
  status, last_error

session_logs     会话数据日志
  id, session_id, ts, direction(tx|rx), data(blob/base64)

audit_logs       审计
  id, user, action, target, detail, ts
```

模板里的 `default_connections` 示例（树莓派 4B）：

```yaml
default_connections:
  - kind: serial
    name: UART 调试串口
    params:
      port: /dev/ttyUSB0        # 实际创建时让用户从枚举列表选
      baudrate: 115200
      bytesize: 8
      parity: N
      stopbits: 1
      flow_control: none
      login_prompt: "login:"
      default_user: pi
  - kind: ssh
    name: SSH
    params: { port: 22, host: "" }   # P2 启用
spec:
  soc: BCM2711
  ram: [1GB, 2GB, 4GB, 8GB]
  gpio_pinout: { uart_tx: 8, uart_rx: 10, gnd: 6 }
```

---

## 五、API 设计（P1 范围）

```
# 设备
GET    /api/devices?group_id=&tag=&keyword=&page=
POST   /api/devices
GET    /api/devices/{id}
PUT    /api/devices/{id}
DELETE /api/devices/{id}
POST   /api/devices/from-template/{template_id}   # 模板一键创建

# 模板
GET    /api/templates
GET    /api/templates/{key}

# 连接配置
GET    /api/devices/{id}/connections
POST   /api/devices/{id}/connections
PUT    /api/connections/{id}
DELETE /api/connections/{id}

# 串口发现与测试
GET    /api/serial/ports                 # 枚举本机串口（含 USB 插拔后的实时列表）
POST   /api/serial/test                  # 试打开并返回 banner，校验参数

# 会话
POST   /api/connections/{id}/open        # → { session_id }
POST   /api/sessions/{id}/close
GET    /api/sessions?status=online
GET    /api/sessions/{id}/logs

# WebSocket
WS     /ws/terminal/{session_id}         # 二进制双向流（xterm.js attach）
WS     /ws/events                        # 状态事件：设备上线/掉线/会话变更
```

---

## 六、第一阶段（MVP）实现方案：串口/USB 连接树莓派 4B

### 6.1 硬件连接（两种方式都支持）

**方式 A：USB 转 TTL 接 GPIO UART（推荐，最常用）**

```
USB-TTL 适配器(CH340/CP2102/FT232)        树莓派 4B
        TXD  ────────────────────────►  GPIO15 (RXD, 物理引脚 10)
        RXD  ◄────────────────────────  GPIO14 (TXD, 物理引脚 8)
        GND  ────────────────────────   GND    (物理引脚 6)
        (VCC 不接！两边各自供电，避免反灌)
```

树莓派侧准备：
1. `/boot/firmware/config.txt`（老系统在 `/boot/config.txt`）加 `enable_uart=1`
2. 启用串口登录控制台：`raspi-config → Interface Options → Serial Port → 控制台 Yes`
3. 默认参数 **115200 8N1**

**方式 B：USB 直连（Gadget 模式，免串口线）**
1. `config.txt` 加 `dtoverlay=dwc2`，`cmdline.txt` 加 `modules-load=dwc2,g_serial`
2. 用 USB 线连 Pi 的 **USB-C 供电口** 到服务器
3. 服务器侧出现 `/dev/ttyACM0`，Pi 侧为 `/dev/ttyGS0`

平台在 `GET /api/serial/ports` 中把两类（ttyUSB* / ttyACM*）都列出来，用户创建连接配置时下拉选择即可。

### 6.2 会话生命周期

```
open ──► connecting ──► online ──► closed
              │            │
              ▼            ▼
            error ◄── 断线检测(读超时/设备消失)
              │
              ▼ 自动重连(指数退避, 可配置开关)
           connecting
```

- 每个 Session 一个 asyncio Task 读串口 → 广播到所有订阅的 WebSocket（支持多人旁观）
- 写方向：WebSocket 收到前端按键 → 写串口；同时落 `session_logs`
- 断线检测：`/dev/ttyUSB0` 消失或 read 异常 → status=error → 事件总线推送 → 前端徽标变红

### 6.3 Web 终端

- 前端 `xterm.js` + `AttachAddon` 对接 `WS /ws/terminal/{id}`，二进制帧直传，延迟 < 100ms
- 支持：多 Tab 多会话、只读旁观模式、终端尺寸同步（resize 事件 → TIOCSWINSZ 语义对串口忽略即可）、本地回显开关（适配 ESP32 类无回显设备，模板里可配）

### 6.4 项目结构（monorepo）

```
tools/
├── backend/
│   ├── app/
│   │   ├── main.py                 # FastAPI 入口
│   │   ├── api/                    # 路由：devices/templates/connections/serial/sessions
│   │   ├── core/                   # 配置、安全、事件总线
│   │   ├── models/                 # SQLAlchemy 模型
│   │   ├── schemas/                # Pydantic
│   │   ├── services/               # 业务逻辑
│   │   ├── connectors/
│   │   │   ├── base.py             # Connector 协议 + 注册表
│   │   │   └── serial_connector.py # ★ P1 核心
│   │   ├── session/                # 会话管理器、重连、日志落盘
│   │   └── ws/                     # terminal / events 网关
│   ├── templates_builtin/          # 内置模板 YAML
│   │   ├── raspberry_pi_4b.yaml
│   │   ├── raspberry_pi_5.yaml
│   │   ├── banana_pi_m4.yaml
│   │   ├── orange_pi_5.yaml
│   │   ├── esp32_devkit.yaml
│   │   └── arduino_uno.yaml
│   ├── tests/
│   └── pyproject.toml
├── frontend/
│   ├── src/
│   │   ├── views/                  # Dashboard/DeviceList/DeviceDetail/Templates/Terminal
│   │   ├── api/                    # axios 封装
│   │   └── components/             # 连接配置动态表单(JSON Schema 驱动)、状态徽标…
│   └── package.json
└── deploy/
    └── docker-compose.yml
```

### 6.5 里程碑（建议 4 个迭代）

| 迭代 | 交付物 | 验收标准 |
|---|---|---|
| M1 基础骨架 | 后端项目 + DB 迁移 + 设备/模板 CRUD API + 内置模板 | curl 全流程建出"树莓派 4B"设备 |
| M2 串口打通 | serial_connector + 会话管理 + /api/serial/ports | 真机打开会话，日志能看到 `login:` |
| M3 前端闭环 | 设备列表/详情/模板库 + Web 终端 | 浏览器里完成登录树莓派并执行 `uname -a` |
| M4 打磨 | 断线重连、会话日志回放、Docker Compose、README | 拔插 USB 线自动恢复；compose 一键起 |

### 6.6 P1 验收清单

- [ ] 用模板创建一台"树莓派 4B"，自动生成串口连接配置（115200 8N1）
- [ ] 下拉列出 `/dev/ttyUSB0`，一键打开会话
- [ ] Web 终端出现 `raspberrypi login:`，输入账号密码正常登录
- [ ] 执行命令回显正常，中文不乱码
- [ ] 拔掉 USB 线 → 10 秒内前端状态变红；插回 → 自动重连（可配置）
- [ ] 会话日志可回放、可下载

---

## 七、内置设备配置模板（首批 6 款）

| 模板 key | 设备 | 默认连接 | 关键参数 |
|---|---|---|---|
| `raspberry_pi_4b` | 树莓派 4B | UART + SSH | 115200 8N1，GPIO14/15，login 控制台 |
| `raspberry_pi_5` | 树莓派 5 | UART + SSH | 115200 8N1（注意 5 代 UART 在专用 3pin 调试座） |
| `banana_pi_m4` | 香蕉派 BPI-M4 | UART | 115200 8N1，调试口丝印 TX/RX/GND |
| `orange_pi_5` | 香橙派 5 | UART | **1500000** 8N1（瑞芯微平台特殊波特率！） |
| `esp32_devkit` | ESP32 开发板 | USB-Serial(CP2102) | 115200 8N1，无登录提示，本地回显关 |
| `arduino_uno` | Arduino Uno | USB-Serial | 9600/115200 8N1，打开会触发复位(DTR) |

模板为 YAML 文件，放 `templates_builtin/`，启动时扫描入库；用户可在前端"另存为"出自定义模板。模板字段含：默认连接数组、引脚说明、登录提示词、常见坑备注（如香橙派的 1500000 波特率）。

---

## 八、风险与注意事项

1. **权限问题**：Linux 下访问 `/dev/ttyUSB*` 需要 `dialout` 组 → 安装文档 + 启动自检提示
2. **多进程抢串口**：同一串口只能一个会话独占 → 会话管理器加锁，前端提示占用者
3. **特殊波特率**：瑞芯微系（香橙派）1500000 需 `termios2` BOTHER 支持，pyserial 在 Linux 已支持，但要写进模板备注防踩坑
4. **数据安全**：会话日志可能含密码（login 时输入的）→ 提供"登录提示词遮蔽"功能，匹配到 `Password:` 后一行不落明文盘
5. **高并发**：单进程 asyncio 足够支撑数百会话；更大规模再引入 worker 分片

---

## 九、项目名称候选 ×10（含吉祥物）

| # | 名称 | 寓意 | 吉祥物 | 设定 |
|---|---|---|---|---|
| 1 | **灵枢 LinkHub** | "灵枢"出自《黄帝内经》，中枢之意，统领全身经络 = 统一连接所有设备 | 🐙 章鱼「**连连**」 | 八条触手各握一种线缆（USB/网线/杜邦线/天线），触手即连接，寓意多协议一手抓 |
| 2 | **联巢 LinkHive** | 蜂巢是最精密的连接结构，六边形格子 = 标准化接口 | 🐝 工蜂「**小巢**」 | 背着工具包穿梭在六角形巢格间，每格住着一种设备，勤劳巡检查状态 |
| 3 | **派桥 PiBridge** | 为树莓派们搭桥，也谐音" π 桥" | 🦦 水獭「**桥桥**」 | 水獭手拉手连成链漂在水面 = 设备串联上线，怀里抱着一颗树莓 |
| 4 | **万联 OmniLink** | Omni = 全，一切连接方式通吃 | 🦎 变色龙「**万万**」 | 随环境变换皮肤颜色 = 自动适配串口/网络/蓝牙各种协议 |
| 5 | **蒲公英 DandeLink** | 种子落到哪里，连接就到哪里 | 🌼 蒲公英精灵「**蒲蒲**」 | 头顶一簇种子伞，每粒种子是一台设备，乘风接入平台 |
| 6 | **磁连 MagConn** | 磁吸即连，靠近即上线 | 🧲 磁铁小子「**吸吸**」 | U 形磁铁身体，跑过的地方设备"啪嗒"自动吸附连线 |
| 7 | **电络 Eelink** | 电鳗放电 = 信号连通，"络"即网络 | ⚡ 电鳗「**小电**」 | 身体发光的电鳗，尾巴一摆点亮一排设备指示灯 |
| 8 | **萤火 FireflyHub** | 萤火虫发光传信 = 设备间通信 | ✨ 萤火虫「**闪闪**」 | 提着小灯笼在机柜间巡逻，发现新设备就闪三下 = 自动发现 |
| 9 | **泊湾 DockBay** | 港湾停靠所有船只 = 平台接入所有设备 | 🦀 寄居蟹「**湾湾**」 | 背着螺丝壳（硬件梗），钳子举着灯塔为进港设备引路 |
| 10 | **译狐 ProtoFox** | 狐狸聪明善变 = 协议翻译官 | 🦊 小狐狸「**译译**」 | 戴耳机的狐狸，左耳进串口字节流，右耳出标准 JSON |

**推荐**：主选 **灵枢 LinkHub**（中文有底蕴、英文直白、章鱼吉祥物延展性强——LOGO、表情包、CLI 彩蛋都好做）；备选派桥 PiBridge（第一阶段主打树莓派，名字应景）。

---

## 十、下一步

1. 确认名称与技术选型 → 初始化 monorepo 骨架（M1）
2. 采购/准备：USB-TTL 线一根、树莓派 4B 一台刷好 Raspberry Pi OS 并开串口控制台
3. 按 6.5 里程碑推进，M3 结束即具备日常使用价值
