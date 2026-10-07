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
    online: bool = False  # 由会话运行态填充


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


class ConnectionOut(ConnectionIn):
    model_config = ConfigDict(from_attributes=True)
    id: int
    device_id: int


# ---------- 串口 ----------
class SerialPortInfo(BaseModel):
    device: str
    description: str
    hwid: str
    is_usb: bool


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
