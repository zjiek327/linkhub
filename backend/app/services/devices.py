"""设备核心操作（本机 API 与集群内部 API 共用）。"""
from sqlalchemy import delete, select
from sqlalchemy.ext.asyncio import AsyncSession

from ..cluster.state import self_node_id, state
from ..events import bus
from ..models import ConnectionProfile, Device, Session, SessionLog, Template


async def publish_device_change(device_id: int, deleted: bool = False) -> None:
    """设备变更广播：驱动集群目录缓存同步（单机模式下无操作）。"""
    if not state.enabled:
        return
    if deleted:
        bus.publish("device_deleted", {"device_id": device_id})
    else:
        from ..cluster.sync import build_device_entry
        entry = await build_device_entry(device_id)
        if entry:
            bus.publish("device_changed", entry)


async def create_device_from_template(db: AsyncSession, template_key: str, name: str,
                                      group_id: int | None = None,
                                      param_overrides: dict | None = None) -> Device | None:
    tpl = (await db.execute(select(Template).where(Template.key == template_key))).scalar_one_or_none()
    if tpl is None:
        return None
    device = Device(name=name, group_id=group_id, template_id=tpl.id,
                    node_id=self_node_id(),
                    description=f"基于模板「{tpl.name}」创建")
    db.add(device)
    await db.flush()
    for idx, conn in enumerate(tpl.default_connections or []):
        params = {**conn.get("params", {}), **(param_overrides or {}).get(str(idx), {}),
                  **(param_overrides or {}).get(idx, {})}
        db.add(ConnectionProfile(
            device_id=device.id, kind=conn["kind"],
            name=conn.get("name", conn["kind"]), params=params,
            enabled=conn.get("enabled", True), node_id=self_node_id(),
        ))
    await db.commit()
    await db.refresh(device)
    await publish_device_change(device.id)
    return device


async def delete_device_cascade(db: AsyncSession, device_id: int) -> bool:
    """删除设备并级联清理连接配置/会话/日志。返回 False = 不存在。"""
    from ..session_manager import manager
    row = await db.get(Device, device_id)
    if row is None:
        return False
    conn_ids = (await db.execute(
        select(ConnectionProfile.id).where(ConnectionProfile.device_id == device_id)
    )).scalars().all()
    active = (await db.execute(
        select(Session.id).where(Session.connection_id.in_(conn_ids or [0]),
                                 Session.closed_at.is_(None))
    )).scalars().all()
    for sid in active:
        await manager.close(sid)
    all_sessions = (await db.execute(
        select(Session.id).where(Session.connection_id.in_(conn_ids or [0]))
    )).scalars().all()
    if all_sessions:
        await db.execute(delete(SessionLog).where(SessionLog.session_id.in_(all_sessions)))
    if conn_ids:
        await db.execute(delete(Session).where(Session.connection_id.in_(conn_ids)))
    await db.delete(row)
    await db.commit()
    await publish_device_change(device_id, deleted=True)
    return True
