"""Pydantic 请求/响应模型。"""
from datetime import datetime
from typing import Any, Literal

from pydantic import BaseModel, ConfigDict, Field


# ---------- 分组 ----------
class GroupIn(BaseModel):
    name: str
    parent_id: int | None = None


class GroupOut(GroupIn):
    model_config = ConfigDict(from_attributes=True)
    id: int


# ---------- 模板 ----------
class TemplateOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)
    id: int
    key: str
    name: str
    category: str
    icon: str
    description: str
    spec: dict[str, Any]
    default_connections: list[dict[str, Any]]
    builtin: bool


class TemplateIn(BaseModel):
    key: str = Field(min_length=1, max_length=64)
    name: str
    category: str = "自定义"
    icon: str = "🧩"
    description: str = ""
    spec: dict[str, Any] = {}
    default_connections: list[dict[str, Any]] = []


# ---------- 设备 ----------
class DeviceIn(BaseModel):
    name: str = Field(min_length=1, max_length=128)
    description: str = ""
    location: str = ""
    owner: str = ""
    tags: list[str] = []
    group_id: int | None = None


class DeviceOut(DeviceIn):
    model_config = ConfigDict(from_attributes=True)
    id: int
    template_id: int | None
    created_at: datetime
    updated_at: datetime
    online: bool = False        # 由会话运行态填充
    node_id: str = "local"      # 归属节点
    node_name: str = ""         # 归属节点显示名
    node_online: bool = True    # 归属节点是否在线（离线→前端置灰）
    # 仅联邦条目（远程设备）带摘要；validation_alias 避开 ORM connections 关系属性防懒加载
    connections: list[dict[str, Any]] = Field(default=[], validation_alias="connections_snapshot")


class DeviceFromTemplateIn(BaseModel):
    name: str = Field(min_length=1, max_length=128)
    group_id: int | None = None
    # 允许套用模板时覆盖默认连接参数，如 {0: {"port": "/dev/ttyUSB1"}}
    param_overrides: dict[int, dict[str, Any]] = {}


class DeviceListOut(BaseModel):
    total: int
    items: list[DeviceOut]


# ---------- 连接配置 ----------
class ConnectionIn(BaseModel):
    kind: str = Field(min_length=1, max_length=32)
    name: str = Field(min_length=1, max_length=128)
    params: dict[str, Any] = {}
    credential_id: int | None = None
    enabled: bool = True
    node_id: str | None = None  # None = 本机；集群下可指向远程节点的物理端口


class ConnectionOut(ConnectionIn):
    model_config = ConfigDict(from_attributes=True)
    id: int
    device_id: int
    node_id: str = "local"


# ---------- 串口 ----------
class SerialPortInfo(BaseModel):
    device: str
    description: str
    hwid: str
    is_usb: bool
    node: str = ""       # 所属节点名（空 = 本机）
    node_id: str = ""


class SerialTestIn(BaseModel):
    port: str
    baudrate: int = 115200
    bytesize: Literal[5, 6, 7, 8] = 8
    parity: Literal["N", "E", "O", "M", "S"] = "N"
    stopbits: Literal[1, 2] = 1
    probe_ms: int = Field(default=800, ge=100, le=5000)


class SerialTestOut(BaseModel):
    ok: bool
    banner: str = ""
    error: str = ""


# ---------- 会话 ----------
class SessionOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)
    id: int
    connection_id: int
    opened_by: str
    opened_at: datetime
    closed_at: datetime | None
    status: str
    last_error: str
    node_id: str = ""  # 会话实际所在节点（空 = 本机）


class SessionLogOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)
    id: int
    ts: datetime
    direction: str
    data: str


class StatsOut(BaseModel):
    devices_total: int
    devices_online: int
    sessions_online: int
    templates_total: int
    nodes_total: int = 0
    nodes_online: int = 0


# ---------- 集群 ----------
class NodeOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)
    node_id: str
    name: str
    address: str
    status: str
    is_self: bool
    last_seen: datetime
    resources: dict[str, Any] = {}


class JoinIn(BaseModel):
    address: str = Field(min_length=1)          # http://192.168.1.10:8000
    token: str | None = None                    # 不传则用本机配置的集群令牌


class HandshakeIn(BaseModel):
    node_id: str
    name: str
    address: str


class DirectoryEntry(BaseModel):
    """设备目录条目：远程设备的完整摘要（用于联邦列表渲染）。"""
    node_id: str
    id: int
    name: str
    description: str = ""
    location: str = ""
    owner: str = ""
    tags: list[str] = []
    group_id: int | None = None
    template_id: int | None = None
    online: bool = False
    created_at: datetime | None = None
    updated_at: datetime | None = None
    connections: list[dict[str, Any]] = []      # [{id, kind, name, node_id, enabled}]


class DiscoveredNode(BaseModel):
    node_id: str
    name: str
    address: str


class RemoteOpenIn(BaseModel):
    connection_id: int


class RemoteOpenOut(BaseModel):
    session_id: int
    node_id: str


class ProxyDeviceOut(BaseModel):
    device: dict[str, Any]
    connections: list[dict[str, Any]]


# ---------- 批量执行 ----------
class BatchTarget(BaseModel):
    node_id: str = "local"
    device_id: int


class BatchExecIn(BaseModel):
    targets: list[BatchTarget]
    command: str = Field(min_length=1)
    wait_ms: int = Field(default=1500, ge=100, le=30000)


class BatchResult(BaseModel):
    node_id: str
    device_id: int
    device_name: str = ""
    ok: bool
    output: str = ""
    error: str = ""


class BatchExecOut(BaseModel):
    results: list[BatchResult]
