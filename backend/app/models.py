"""SQLAlchemy 数据模型。"""
from datetime import datetime, timezone

from sqlalchemy import JSON, Boolean, DateTime, ForeignKey, Integer, String, Text
from sqlalchemy.orm import DeclarativeBase, Mapped, mapped_column, relationship


def utcnow() -> datetime:
    return datetime.now(timezone.utc)


class Base(DeclarativeBase):
    pass


class Meta(Base):
    """键值元数据（node_id 持久化等）。"""
    __tablename__ = "meta"

    key: Mapped[str] = mapped_column(String(64), primary_key=True)
    value: Mapped[str] = mapped_column(Text, default="")


class Node(Base):
    """集群节点（含本机，is_self 区分）。"""
    __tablename__ = "nodes"

    node_id: Mapped[str] = mapped_column(String(32), primary_key=True)  # n-xxxxxx
    name: Mapped[str] = mapped_column(String(128), default="")
    address: Mapped[str] = mapped_column(String(256), default="")       # http://ip:port
    status: Mapped[str] = mapped_column(String(16), default="online")   # online / offline
    is_self: Mapped[bool] = mapped_column(Boolean, default=False)
    last_seen: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)
    resources: Mapped[dict] = mapped_column(JSON, default=dict)         # {serial_ports: [...]}


class DeviceGroup(Base):
    __tablename__ = "device_groups"

    id: Mapped[int] = mapped_column(primary_key=True)
    name: Mapped[str] = mapped_column(String(128), unique=True, index=True)
    parent_id: Mapped[int | None] = mapped_column(ForeignKey("device_groups.id"), nullable=True)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)

    devices: Mapped[list["Device"]] = relationship(back_populates="group")


class Template(Base):
    __tablename__ = "templates"

    id: Mapped[int] = mapped_column(primary_key=True)
    key: Mapped[str] = mapped_column(String(64), unique=True, index=True)  # raspberry_pi_4b
    name: Mapped[str] = mapped_column(String(128))
    category: Mapped[str] = mapped_column(String(64), default="开发板")
    icon: Mapped[str] = mapped_column(String(32), default="🧩")
    description: Mapped[str] = mapped_column(Text, default="")
    spec: Mapped[dict] = mapped_column(JSON, default=dict)                  # 硬件规格/引脚/备注
    default_connections: Mapped[list] = mapped_column(JSON, default=list)  # 默认连接配置数组
    builtin: Mapped[bool] = mapped_column(Boolean, default=False)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)


class Device(Base):
    __tablename__ = "devices"

    id: Mapped[int] = mapped_column(primary_key=True)
    name: Mapped[str] = mapped_column(String(128), index=True)
    description: Mapped[str] = mapped_column(Text, default="")
    location: Mapped[str] = mapped_column(String(256), default="")
    owner: Mapped[str] = mapped_column(String(128), default="")
    tags: Mapped[list] = mapped_column(JSON, default=list)
    group_id: Mapped[int | None] = mapped_column(ForeignKey("device_groups.id"), nullable=True)
    template_id: Mapped[int | None] = mapped_column(ForeignKey("templates.id"), nullable=True)
    node_id: Mapped[str] = mapped_column(String(32), default="local", index=True)  # 归属节点
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)
    updated_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow, onupdate=utcnow)

    group: Mapped[DeviceGroup | None] = relationship(back_populates="devices")
    template: Mapped[Template | None] = relationship()
    connections: Mapped[list["ConnectionProfile"]] = relationship(
        back_populates="device", cascade="all, delete-orphan"
    )


class Credential(Base):
    __tablename__ = "credentials"

    id: Mapped[int] = mapped_column(primary_key=True)
    name: Mapped[str] = mapped_column(String(128), unique=True)
    type: Mapped[str] = mapped_column(String(32), default="password")  # password | key
    secret_enc: Mapped[str] = mapped_column(Text)  # AES-GCM 密文(base64)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)


class ConnectionProfile(Base):
    __tablename__ = "connection_profiles"

    id: Mapped[int] = mapped_column(primary_key=True)
    device_id: Mapped[int] = mapped_column(ForeignKey("devices.id"), index=True)
    kind: Mapped[str] = mapped_column(String(32))  # serial | ssh | ble | ...
    name: Mapped[str] = mapped_column(String(128))
    params: Mapped[dict] = mapped_column(JSON, default=dict)
    node_id: Mapped[str] = mapped_column(String(32), default="local", index=True)  # 物理端口所在节点
    credential_id: Mapped[int | None] = mapped_column(ForeignKey("credentials.id"), nullable=True)
    enabled: Mapped[bool] = mapped_column(Boolean, default=True)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)

    device: Mapped[Device] = relationship(back_populates="connections")
    credential: Mapped[Credential | None] = relationship()
    sessions: Mapped[list["Session"]] = relationship(
        back_populates="connection", cascade="all, delete-orphan"
    )


class Session(Base):
    __tablename__ = "sessions"

    id: Mapped[int] = mapped_column(primary_key=True)
    connection_id: Mapped[int | None] = mapped_column(
        ForeignKey("connection_profiles.id"), index=True, nullable=True)  # None = 内联临时会话
    opened_by: Mapped[str] = mapped_column(String(128), default="admin")
    opened_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)
    closed_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    status: Mapped[str] = mapped_column(String(32), default="connecting")  # connecting/online/closed/error
    last_error: Mapped[str] = mapped_column(Text, default="")

    connection: Mapped[ConnectionProfile] = relationship(back_populates="sessions")


class SessionLog(Base):
    __tablename__ = "session_logs"

    id: Mapped[int] = mapped_column(primary_key=True)
    session_id: Mapped[int] = mapped_column(ForeignKey("sessions.id"), index=True)
    ts: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)
    direction: Mapped[str] = mapped_column(String(8))  # tx | rx | meta
    data: Mapped[str] = mapped_column(Text)  # base64 或文本(meta)


class AuditLog(Base):
    __tablename__ = "audit_logs"

    id: Mapped[int] = mapped_column(primary_key=True)
    user: Mapped[str] = mapped_column(String(128), default="admin")
    action: Mapped[str] = mapped_column(String(64))
    target: Mapped[str] = mapped_column(String(256), default="")
    detail: Mapped[str] = mapped_column(Text, default="")
    ts: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)


class User(Base):
    __tablename__ = "users"

    id: Mapped[int] = mapped_column(primary_key=True)
    name: Mapped[str] = mapped_column(String(128), unique=True, index=True)
    password_hash: Mapped[str] = mapped_column(String(256))
    role: Mapped[str] = mapped_column(String(16), default="viewer")  # admin/operator/viewer
    enabled: Mapped[bool] = mapped_column(Boolean, default=True)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)


class ScheduledTask(Base):
    """P4 定时任务：周期对多设备执行命令。"""
    __tablename__ = "scheduled_tasks"

    id: Mapped[int] = mapped_column(primary_key=True)
    name: Mapped[str] = mapped_column(String(128))
    command: Mapped[str] = mapped_column(Text)
    targets: Mapped[list] = mapped_column(JSON, default=list)   # [{node_id, device_id}]
    interval_s: Mapped[int] = mapped_column(Integer, default=300)
    enabled: Mapped[bool] = mapped_column(Boolean, default=True)
    wait_ms: Mapped[int] = mapped_column(Integer, default=1500)
    last_run: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    history: Mapped[list] = mapped_column(JSON, default=list)   # 最近 20 次结果
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)
