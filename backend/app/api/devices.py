"""设备 / 分组 / 统计 API。"""
from fastapi import APIRouter, Depends, HTTPException, Query
from sqlalchemy import delete, func, or_, select
from sqlalchemy.ext.asyncio import AsyncSession

from ..database import get_db
from ..models import ConnectionProfile, Device, DeviceGroup, Session, SessionLog, Template
from ..schemas import (DeviceFromTemplateIn, DeviceIn, DeviceListOut, DeviceOut,
                       GroupIn, GroupOut, StatsOut)
from ..session_manager import manager

router = APIRouter(prefix="/api", tags=["devices"])


# ---------- 分组 ----------
@router.get("/groups", response_model=list[GroupOut])
async def list_groups(db: AsyncSession = Depends(get_db)):
    return (await db.execute(select(DeviceGroup).order_by(DeviceGroup.id))).scalars().all()


@router.post("/groups", response_model=GroupOut, status_code=201)
async def create_group(body: GroupIn, db: AsyncSession = Depends(get_db)):
    row = DeviceGroup(**body.model_dump())
    db.add(row)
    await db.commit()
    await db.refresh(row)
    return row


@router.delete("/groups/{group_id}", status_code=204)
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
@router.get("/devices", response_model=DeviceListOut)
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
    for d in rows[(page - 1) * size: page * size]:
        out = DeviceOut.model_validate(d)
        out.online = d.id in online_ids
        items.append(out)
    return DeviceListOut(total=len(rows), items=items)


@router.post("/devices", response_model=DeviceOut, status_code=201)
async def create_device(body: DeviceIn, db: AsyncSession = Depends(get_db)):
    row = Device(**body.model_dump())
    db.add(row)
    await db.commit()
    await db.refresh(row)
    return row


@router.get("/devices/{device_id}", response_model=DeviceOut)
async def get_device(device_id: int, db: AsyncSession = Depends(get_db)):
    row = await db.get(Device, device_id)
    if row is None:
        raise HTTPException(404, "设备不存在")
    out = DeviceOut.model_validate(row)
    out.online = row.id in manager.online_device_ids()
    return out


@router.put("/devices/{device_id}", response_model=DeviceOut)
async def update_device(device_id: int, body: DeviceIn, db: AsyncSession = Depends(get_db)):
    row = await db.get(Device, device_id)
    if row is None:
        raise HTTPException(404, "设备不存在")
    for k, v in body.model_dump().items():
        setattr(row, k, v)
    await db.commit()
    await db.refresh(row)
    return row


@router.delete("/devices/{device_id}", status_code=204)
async def delete_device(device_id: int, db: AsyncSession = Depends(get_db)):
    row = await db.get(Device, device_id)
    if row is None:
        raise HTTPException(404, "设备不存在")
    # 关闭该设备所有活跃会话
    conn_ids = (await db.execute(
        select(ConnectionProfile.id).where(ConnectionProfile.device_id == device_id)
    )).scalars().all()
    active = (await db.execute(
        select(Session.id).where(Session.connection_id.in_(conn_ids or [0]), Session.closed_at.is_(None))
    )).scalars().all()
    for sid in active:
        await manager.close(sid)
    # 显式清理会话与日志（连接配置由 ORM 级联删除）
    all_sessions = (await db.execute(
        select(Session.id).where(Session.connection_id.in_(conn_ids or [0]))
    )).scalars().all()
    if all_sessions:
        await db.execute(delete(SessionLog).where(SessionLog.session_id.in_(all_sessions)))
    if conn_ids:
        await db.execute(delete(Session).where(Session.connection_id.in_(conn_ids)))
    await db.delete(row)
    await db.commit()


@router.post("/devices/from-template/{template_key}", response_model=DeviceOut, status_code=201)
async def create_from_template(template_key: str, body: DeviceFromTemplateIn,
                               db: AsyncSession = Depends(get_db)):
    tpl = (await db.execute(select(Template).where(Template.key == template_key))).scalar_one_or_none()
    if tpl is None:
        raise HTTPException(404, f"模板 {template_key} 不存在")
    device = Device(name=body.name, group_id=body.group_id, template_id=tpl.id,
                    description=f"基于模板「{tpl.name}」创建")
    db.add(device)
    await db.flush()
    for idx, conn in enumerate(tpl.default_connections or []):
        params = {**conn.get("params", {}), **body.param_overrides.get(idx, {})}
        db.add(ConnectionProfile(
            device_id=device.id, kind=conn["kind"],
            name=conn.get("name", conn["kind"]), params=params,
            enabled=conn.get("enabled", True),
        ))
    await db.commit()
    await db.refresh(device)
    return device


# ---------- 统计 ----------
@router.get("/stats", response_model=StatsOut)
async def stats(db: AsyncSession = Depends(get_db)):
    devices_total = (await db.execute(select(func.count(Device.id)))).scalar_one()
    templates_total = (await db.execute(select(func.count(Template.id)))).scalar_one()
    return StatsOut(
        devices_total=devices_total,
        devices_online=len(manager.online_device_ids()),
        sessions_online=len(manager.online_session_ids()),
        templates_total=templates_total,
    )
