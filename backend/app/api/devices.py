"""设备 / 分组 / 统计 API。"""
from fastapi import APIRouter, Depends, HTTPException, Query
from sqlalchemy import delete, func, or_, select
from sqlalchemy.ext.asyncio import AsyncSession

from ..auth import require_operator, require_viewer
from ..cluster.state import self_node_id, state
from ..cluster.sync import build_device_entry
from ..database import get_db
from ..events import bus
from ..models import ConnectionProfile, Device, DeviceGroup, Node, Session, SessionLog, Template
from ..schemas import (DeviceFromTemplateIn, DeviceIn, DeviceListOut, DeviceOut,
                       GroupIn, GroupOut, StatsOut)
from ..session_manager import manager

router = APIRouter(prefix="/api", tags=["devices"])


async def _publish_device(device_id: int, deleted: bool = False) -> None:
    """设备变更广播：驱动集群目录缓存同步（单机模式下无操作）。"""
    if not state.enabled:
        return
    if deleted:
        bus.publish("device_deleted", {"device_id": device_id})
    else:
        entry = await build_device_entry(device_id)
        if entry:
            bus.publish("device_changed", entry)


# ---------- 分组 ----------
@router.get("/groups", response_model=list[GroupOut])
async def list_groups(db: AsyncSession = Depends(get_db)):
    return (await db.execute(select(DeviceGroup).order_by(DeviceGroup.id))).scalars().all()


@router.post("/groups", response_model=GroupOut, status_code=201, dependencies=[Depends(require_operator)])
async def create_group(body: GroupIn, db: AsyncSession = Depends(get_db)):
    row = DeviceGroup(**body.model_dump())
    db.add(row)
    await db.commit()
    await db.refresh(row)
    return row


@router.delete("/groups/{group_id}", status_code=204, dependencies=[Depends(require_operator)])
async def delete_group(group_id: int, db: AsyncSession = Depends(get_db)):
    row = await db.get(DeviceGroup, group_id)
    if row is None:
        raise HTTPException(404, "分组不存在")
    await db.execute(
        Device.__table__.update().where(Device.group_id == group_id).values(group_id=None)
    )
    await db.delete(row)
    await db.commit()


# ---------- 设备 ----------
@router.get("/devices", response_model=DeviceListOut, dependencies=[Depends(require_viewer)])
async def list_devices(
    group_id: int | None = None,
    tag: str | None = None,
    keyword: str | None = None,
    page: int = Query(1, ge=1),
    size: int = Query(20, ge=1, le=200),
    db: AsyncSession = Depends(get_db),
):
    stmt = select(Device)
    if group_id is not None:
        stmt = stmt.where(Device.group_id == group_id)
    if keyword:
        like = f"%{keyword}%"
        stmt = stmt.where(or_(Device.name.like(like), Device.description.like(like)))
    rows = (await db.execute(stmt.order_by(Device.id.desc()))).scalars().all()
    if tag:
        rows = [d for d in rows if tag in (d.tags or [])]
    online_ids = manager.online_device_ids()
    items = []
    for d in rows:
        out = DeviceOut.model_validate(d)
        out.online = d.id in online_ids
        out.node_name = state.self_name if state.enabled else ""
        items.append(out)

    # 联邦：合入远程节点设备摘要
    if state.enabled:
        for entry in state.directory.values():
            if group_id is not None and entry.get("group_id") != group_id:
                continue
            if tag and tag not in (entry.get("tags") or []):
                continue
            if keyword and keyword not in (entry.get("name") or "") + (entry.get("description") or ""):
                continue
            peer = state.peers.get(entry["node_id"])
            items.append(DeviceOut(
                id=entry["id"], name=entry["name"],
                description=entry.get("description", ""), location=entry.get("location", ""),
                owner=entry.get("owner", ""), tags=entry.get("tags", []),
                group_id=entry.get("group_id"), template_id=entry.get("template_id"),
                created_at=entry.get("created_at") or "1970-01-01T00:00:00",
                updated_at=entry.get("updated_at") or "1970-01-01T00:00:00",
                online=entry.get("online", False),
                node_id=entry["node_id"],
                node_name=peer.name if peer else entry["node_id"],
                node_online=peer.status == "online" if peer else False,
                connections_snapshot=entry.get("connections", []),
            ))
    total = len(items)
    return DeviceListOut(total=total, items=items[(page - 1) * size: page * size])


@router.post("/devices", response_model=DeviceOut, status_code=201, dependencies=[Depends(require_operator)])
async def create_device(body: DeviceIn, db: AsyncSession = Depends(get_db)):
    row = Device(**body.model_dump(), node_id=self_node_id())
    db.add(row)
    await db.commit()
    await db.refresh(row)
    await _publish_device(row.id)
    return row


@router.get("/devices/{device_id}", response_model=DeviceOut, dependencies=[Depends(require_viewer)])
async def get_device(device_id: int, db: AsyncSession = Depends(get_db)):
    row = await db.get(Device, device_id)
    if row is None:
        raise HTTPException(404, "设备不存在")
    out = DeviceOut.model_validate(row)
    out.online = row.id in manager.online_device_ids()
    return out


@router.put("/devices/{device_id}", response_model=DeviceOut, dependencies=[Depends(require_operator)])
async def update_device(device_id: int, body: DeviceIn, db: AsyncSession = Depends(get_db)):
    row = await db.get(Device, device_id)
    if row is None:
        raise HTTPException(404, "设备不存在")
    for k, v in body.model_dump().items():
        setattr(row, k, v)
    await db.commit()
    await db.refresh(row)
    await _publish_device(row.id)
    return row


@router.delete("/devices/{device_id}", status_code=204, dependencies=[Depends(require_operator)])
async def delete_device(device_id: int, db: AsyncSession = Depends(get_db)):
    from ..services.devices import delete_device_cascade
    if not await delete_device_cascade(db, device_id):
        raise HTTPException(404, "设备不存在")


@router.post("/devices/from-template/{template_key}", response_model=DeviceOut, status_code=201, dependencies=[Depends(require_operator)])
async def create_from_template(template_key: str, body: DeviceFromTemplateIn,
                               db: AsyncSession = Depends(get_db)):
    from ..services.devices import create_device_from_template
    device = await create_device_from_template(db, template_key, body.name,
                                               body.group_id, body.param_overrides)
    if device is None:
        raise HTTPException(404, f"模板 {template_key} 不存在")
    return device


# ---------- 统计 ----------
@router.get("/stats", response_model=StatsOut)
async def stats(db: AsyncSession = Depends(get_db)):
    devices_total = (await db.execute(select(func.count(Device.id)))).scalar_one()
    templates_total = (await db.execute(select(func.count(Template.id)))).scalar_one()
    devices_online = len(manager.online_device_ids())
    nodes_total = nodes_online = 0
    if state.enabled:
        nodes_total = (await db.execute(select(func.count(Node.node_id)))).scalar_one()
        nodes_online = 1 + sum(1 for p in state.peers.values() if p.status == "online")
        devices_total += len(state.directory)
        devices_online += sum(1 for e in state.directory.values() if e.get("online"))
    return StatsOut(
        devices_total=devices_total,
        devices_online=devices_online,
        sessions_online=len(manager.online_session_ids()),
        templates_total=templates_total,
        nodes_total=nodes_total,
        nodes_online=nodes_online,
    )
