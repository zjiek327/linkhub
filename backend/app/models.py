"""SQLAlchemy 数据模型。"""
from datetime import datetime, timezone

from sqlalchemy import JSON, Boolean, DateTime, ForeignKey, Integer, String, Text
from sqlalchemy.orm import DeclarativeBase, Mapped, mapped_column, relationship


def utcnow() -> datetime:
    return datetime.now(timezone.utc)


class Base(DeclarativeBase):
    pass


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
    connection_id: Mapped[int] = mapped_column(ForeignKey("connection_profiles.id"), index=True)
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
