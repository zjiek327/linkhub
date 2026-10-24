# 节点桌面共享（VNC）

LinkHub 支持在浏览器里远程查看/操作集群节点的图形桌面，Ubuntu 与 Windows 节点可互相访问。

## 原理

```
浏览器（noVNC）── WebSocket ──> LinkHub 节点 /ws/desktop ── TCP ──> 目标节点 127.0.0.1:5900（VNC 服务端）
                                   │
                                   └─ 跨节点：沿集群路由中继（对齐终端中继，最多 3 跳）
```

协议统一为 RFB（VNC）。LinkHub 只做代理与能力探测，**不内置桌面服务端**——节点需自行运行 VNC 服务端。

## 前提：节点安装 VNC 服务端

| 节点 | 做法 |
|---|---|
| Ubuntu + X11 | `bash scripts/setup-desktop-share.sh`（自动探测，加 `--apply` 直接装 x11vnc 并启动） |
| Ubuntu 24.04+ Wayland（GNOME 46+） | 设置 → 共享 → 桌面共享 → 选 **VNC** 模式并设密码 |
| Ubuntu Wayland（GNOME < 46） | 系统自带共享只有 RDP，浏览器不可用 → 改 X11 会话用 x11vnc |
| Windows | 安装 TightVNC / UltraVNC 服务端，监听 5900，设置密码 |
| 无桌面（Server 版） | v1 不支持（前端按钮置灰） |

端口默认 5900，可用环境变量 `LINKHUB_DESKTOP_PORT` 改（需与 VNC 服务端实际监听端口一致）。

## 使用

1. 「集群」页节点列表新增「桌面」列，三态：
   - **🖥 桌面**（绿色）= VNC 服务运行中，点击 → 输入 VNC 密码 → 进入桌面
   - **需开启** = 有图形会话但没起 VNC 服务，点击弹出对应平台的开启指引
   - **无桌面**（置灰）= headless 节点
2. 集群未启用时，页面顶部显示「本机桌面」卡片，能力独立探测，同样可连。
3. 桌面窗口工具栏：Ctrl+Alt+Del、全屏、断开。

## 能力广播机制

- 本机探测 `internal/desktop`（15s 缓存）：先探 5900 端口监听 → `active`；否则探图形会话（X11 socket / Wayland socket / 桌面进程）→ `available`；都没有 → `headless`
- 能力随 `ResourceWatchLoop` 进 nodes.resources，心跳 ping 时带给对端，节点列表实时可见
- 探测高频调用安全：端口探测有 300ms 超时，结果缓存 15s

## 安全说明

- `/ws/desktop` 浏览器直连需登录 JWT（query token）；节点间中继用集群令牌（`X-LinkHub-Token`）
- RFB 协议只验证密码、流量不加密——**仅限可信内网**。跨节点中继段是明文转发，后续版本考虑 TLS
- VNC 密码每次连接手输，不落库
