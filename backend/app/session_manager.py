"""会话管理器：一个活跃连接 = 一个 SessionRuntime。

职责：连接器生命周期、读写循环、订阅广播、日志落盘、断线检测与指数退避重连、
密码输入遮蔽（检测到 Password: 提示后，tx 日志遮蔽至换行）。
"""
import asyncio
import base64
import contextlib
import time
from dataclasses import dataclass, field
from datetime import datetime, timezone

from sqlalchemy import select

from .config import get_settings
from .connectors import Connector, ConnectorError, create_connector
from .database import SessionLocal
from .events import bus
from .models import ConnectionProfile, Session, SessionLog

MASK_AFTER = (b"assword:", b"PIN:")  # 触发遮蔽的提示词


@dataclass
class SessionRuntime:
    session_id: int
    device_id: int
    connector: Connector
    params: dict
    task: asyncio.Task | None = None
    subscribers: set[asyncio.Queue] = field(default_factory=set)
    status: str = "connecting"
    last_error: str = ""
    _masking: bool = False
    _closing: bool = False


class SessionManager:
    def __init__(self) -> None:
        self._runtimes: dict[int, SessionRuntime] = {}
        self._port_locks: set[str] = set()  # 同一物理端口只允许一个会话

    # ---------- 查询 ----------
    def get(self, session_id: int) -> SessionRuntime | None:
        return self._runtimes.get(session_id)

    def online_device_ids(self) -> set[int]:
        return {rt.device_id for rt in self._runtimes.values() if rt.status == "online"}

    def online_session_ids(self) -> set[int]:
        return {sid for sid, rt in self._runtimes.items() if rt.status == "online"}

    # ---------- 打开/关闭 ----------
    async def open(self, connection_id: int, opened_by: str = "admin") -> Session:
        settings = get_settings()
        async with SessionLocal() as db:
            profile = await db.get(ConnectionProfile, connection_id)
            if profile is None:
                raise ConnectorError(f"连接配置 {connection_id} 不存在")
            if not profile.enabled:
                raise ConnectorError(f"连接配置 {profile.name} 已禁用")
            lock_key = f"{profile.kind}:{profile.params.get('port', connection_id)}"
            if lock_key in self._port_locks:
                raise ConnectorError("该端口已被其他会话占用，请先关闭对应会话")

            row = Session(connection_id=connection_id, opened_by=opened_by, status="connecting")
            db.add(row)
            await db.commit()
            await db.refresh(row)

            connector = create_connector(profile.kind, profile.params)
            rt = SessionRuntime(
                session_id=row.id,
                device_id=profile.device_id,
                connector=connector,
                params=dict(profile.params),
            )
            self._runtimes[row.id] = rt
            self._port_locks.add(lock_key)
            rt.task = asyncio.create_task(self._run(rt, profile.params, settings))
            return row

    async def close(self, session_id: int) -> bool:
        rt = self._runtimes.get(session_id)
        if rt is None:
            return False
        rt._closing = True
        if rt.task:
            rt.task.cancel()
            with contextlib.suppress(asyncio.CancelledError):
                await rt.task
        await self._finalize(rt, "closed")
        return True

    async def close_all(self) -> None:
        for sid in list(self._runtimes):
            await self.close(sid)

    # ---------- 写入与订阅 ----------
    async def write(self, session_id: int, data: bytes) -> None:
        rt = self._runtimes.get(session_id)
        if rt is None or rt.status != "online":
            raise ConnectorError("会话不在线")
        await rt.connector.write(data)
        log_data = b"******" if rt._masking else data
        # shield：防止用户在写入瞬间关闭终端导致日志丢失
        await asyncio.shield(self._log(session_id, "tx", log_data))
        if data in (b"\r", b"\n") or data.endswith(b"\n"):
            rt._masking = False

    def subscribe(self, session_id: int) -> asyncio.Queue:
        rt = self._runtimes[session_id]
        q: asyncio.Queue = asyncio.Queue(maxsize=512)
        rt.subscribers.add(q)
        return q

    def unsubscribe(self, session_id: int, q: asyncio.Queue) -> None:
        rt = self._runtimes.get(session_id)
        if rt:
            rt.subscribers.discard(q)

    # ---------- 内部：运行循环 ----------
    async def _run(self, rt: SessionRuntime, params: dict, settings) -> None:
        delay = 1.0
        while True:
            try:
                await self._set_status(rt, "connecting")
                await rt.connector.open()
                await self._set_status(rt, "online")
                delay = 1.0  # 成功连接后重置退避
                await self._pump(rt)
                # 正常 EOF（_pump 返回）也视为断开
                raise ConnectorError("连接已断开")
            except asyncio.CancelledError:
                raise
            except Exception as exc:
                if rt._closing:
                    return
                with contextlib.suppress(Exception):  # 清掉半开连接再重试
                    await rt.connector.close()
                await self._set_status(rt, "error", str(exc))
                if not (settings.reconnect_enabled and params.get("auto_reconnect", True)):
                    await self._finalize(rt, "error")
                    return
                await asyncio.sleep(delay)
                delay = min(delay * 2, settings.reconnect_max_delay)

    async def _pump(self, rt: SessionRuntime) -> None:
        async for chunk in rt.connector.read():
            tail = chunk[-32:]
            if any(p in tail for p in MASK_AFTER):
                rt._masking = True
            for q in list(rt.subscribers):
                if not q.full():
                    q.put_nowait(chunk)
            await asyncio.shield(self._log(rt.session_id, "rx", chunk))

    # ---------- 内部：状态/日志 ----------
    async def _set_status(self, rt: SessionRuntime, status: str, error: str = "") -> None:
        rt.status, rt.last_error = status, error
        async with SessionLocal() as db:
            row = await db.get(Session, rt.session_id)
            if row:
                row.status = status
                row.last_error = error
                await db.commit()
        bus.publish("session_status", {
            "session_id": rt.session_id, "device_id": rt.device_id,
            "status": status, "error": error, "ts": time.time(),
        })

    async def _finalize(self, rt: SessionRuntime, status: str) -> None:
        with contextlib.suppress(Exception):
            await rt.connector.close()
        async with SessionLocal() as db:
            row = await db.get(Session, rt.session_id)
            if row and row.closed_at is None:
                row.status = "closed" if status == "closed" else row.status
                row.closed_at = datetime.now(timezone.utc)
                await db.commit()
        bus.publish("session_status", {
            "session_id": rt.session_id, "device_id": rt.device_id,
            "status": "closed", "error": rt.last_error, "ts": time.time(),
        })
        lock_key = f"{rt.connector.kind}:{rt.params.get('port', rt.session_id)}"
        self._port_locks.discard(lock_key)
        self._runtimes.pop(rt.session_id, None)

    async def _log(self, session_id: int, direction: str, data: bytes) -> None:
        async with SessionLocal() as db:
            db.add(SessionLog(
                session_id=session_id, direction=direction,
                data=base64.b64encode(data).decode(),
            ))
            await db.commit()


manager = SessionManager()
