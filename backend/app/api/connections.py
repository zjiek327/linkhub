"""连接配置 + 会话控制 API。"""
from fastapi import APIRouter, Depends, HTTPException
from sqlalchemy import delete, select
from sqlalchemy.ext.asyncio import AsyncSession

from ..connectors import ConnectorError, list_kinds
from ..database import get_db
from ..models import ConnectionProfile, Device, Session, SessionLog
from ..schemas import ConnectionIn, ConnectionOut, SessionOut
from ..session_manager import manager

router = APIRouter(prefix="/api", tags=["connections"])


@router.get("/connector-kinds")
async def connector_kinds():
    """列出所有已注册连接器及其参数 JSON Schema（前端动态表单用）。"""
    return list_kinds()


@router.get("/devices/{device_id}/connections", response_model=list[ConnectionOut])
async def list_connections(device_id: int, db: AsyncSession = Depends(get_db)):
    if await db.get(Device, device_id) is None:
        raise HTTPException(404, "设备不存在")
    return (await db.execute(
        select(ConnectionProfile).where(ConnectionProfile.device_id == device_id)
    )).scalars().all()


@router.post("/devices/{device_id}/connections", response_model=ConnectionOut, status_code=201)
async def create_connection(device_id: int, body: ConnectionIn, db: AsyncSession = Depends(get_db)):
    if await db.get(Device, device_id) is None:
        raise HTTPException(404, "设备不存在")
    row = ConnectionProfile(device_id=device_id, **body.model_dump())
    db.add(row)
    await db.commit()
    await db.refresh(row)
    return row


@router.put("/connections/{conn_id}", response_model=ConnectionOut)
async def update_connection(conn_id: int, body: ConnectionIn, db: AsyncSession = Depends(get_db)):
    row = await db.get(ConnectionProfile, conn_id)
    if row is None:
        raise HTTPException(404, "连接配置不存在")
    for k, v in body.model_dump().items():
        setattr(row, k, v)
    await db.commit()
    await db.refresh(row)
    return row


@router.delete("/connections/{conn_id}", status_code=204)
async def delete_connection(conn_id: int, db: AsyncSession = Depends(get_db)):
    row = await db.get(ConnectionProfile, conn_id)
    if row is None:
        raise HTTPException(404, "连接配置不存在")
    sess_ids = (await db.execute(
        select(Session.id).where(Session.connection_id == conn_id)
    )).scalars().all()
    for sid in sess_ids:
        await manager.close(sid)
    if sess_ids:
        await db.execute(delete(SessionLog).where(SessionLog.session_id.in_(sess_ids)))
        await db.execute(delete(Session).where(Session.id.in_(sess_ids)))
    await db.delete(row)
    await db.commit()


@router.post("/connections/{conn_id}/open", response_model=SessionOut, status_code=201)
async def open_session(conn_id: int, db: AsyncSession = Depends(get_db)):
    if await db.get(ConnectionProfile, conn_id) is None:
        raise HTTPException(404, "连接配置不存在")
    try:
        return await manager.open(conn_id)
    except ConnectorError as exc:
        raise HTTPException(409, str(exc)) from exc
