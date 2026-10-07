"""批量命令执行（C3）：向多台设备下发同一命令，收集输出窗口。

本机执行单元；跨节点由 API 层按归属节点分组后 RPC 分发、合并结果。
"""
import asyncio

from sqlalchemy import select

from .database import SessionLocal
from .models import ConnectionProfile, Device
from .session_manager import manager


async def batch_exec_local(device_ids: list[int], command: str, wait_ms: int,
                           node_id: str = "local") -> list[dict]:
    async with SessionLocal() as db:
        devices = {d.id: d for d in (await db.execute(
            select(Device).where(Device.id.in_(device_ids)))).scalars().all()}
        conns = {c.device_id: c for c in (await db.execute(
            select(ConnectionProfile)
            .where(ConnectionProfile.device_id.in_(device_ids), ConnectionProfile.enabled.is_(True))
            .order_by(ConnectionProfile.id))).scalars().all()}  # 同设备多个时取第一个启用的

    results = []
    for dev_id in device_ids:
        device, conn = devices.get(dev_id), conns.get(dev_id)
        base = {"node_id": node_id, "device_id": dev_id,
                "device_name": device.name if device else f"#{dev_id}"}
        if device is None:
            results.append({**base, "ok": False, "output": "", "error": "设备不存在"})
            continue
        if conn is None:
            results.append({**base, "ok": False, "output": "", "error": "无启用的连接配置"})
            continue
        results.append({**base, **await _exec_on_connection(conn.id, command, wait_ms)})
    return results


async def _exec_on_connection(conn_id: int, command: str, wait_ms: int) -> dict:
    sid, temp = None, False
    try:
        rt = manager.find_online_by_connection(conn_id)
        if rt is None:
            row = await manager.open(conn_id, opened_by="batch")
            sid, temp = row.id, True
            deadline = asyncio.get_running_loop().time() + 5
            while asyncio.get_running_loop().time() < deadline:
                rt = manager.get(sid)
                if rt and rt.status == "online":
                    break
                if rt and rt.status == "error":
                    return {"ok": False, "output": "", "error": rt.last_error}
                await asyncio.sleep(0.1)
            else:
                return {"ok": False, "output": "", "error": "会话上线超时"}
        else:
            sid = rt.session_id

        q, _ = manager.subscribe(sid, f"batch-{sid}-{id(command)}",
                                 name="批量执行", passive=True)
        try:
            await manager.write(sid, command.encode() + b"\r")
            chunks, deadline = [], asyncio.get_running_loop().time() + wait_ms / 1000
            while True:
                left = deadline - asyncio.get_running_loop().time()
                if left <= 0:
                    break
                try:
                    item = await asyncio.wait_for(q.get(), timeout=left)
                    if isinstance(item, bytes):  # 控制消息（dict）不算输出
                        chunks.append(item)
                except TimeoutError:
                    break
            return {"ok": True, "output": b"".join(chunks).decode(errors="replace"), "error": ""}
        finally:
            manager.unsubscribe(sid, f"batch-{sid}-{id(command)}")
    except Exception as exc:
        return {"ok": False, "output": "", "error": str(exc)}
    finally:
        if temp and sid is not None:
            await manager.close(sid)
