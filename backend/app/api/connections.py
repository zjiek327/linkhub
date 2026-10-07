"""连接配置 + 会话控制 API。"""
from datetime import datetime, timezone

import httpx
from fastapi import APIRouter, Depends, HTTPException
from sqlalchemy import delete, select
from sqlalchemy.ext.asyncio import AsyncSession

from ..cluster import client as cluster_client
from ..cluster.state import self_node_id, state
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
    data = body.model_dump()
    data["node_id"] = body.node_id or self_node_id()
    if data["node_id"] not in (self_node_id(), "local") and data["node_id"] not in state.peers:
        raise HTTPException(400, f"节点 {data['node_id']} 不存在")
    row = ConnectionProfile(device_id=device_id, **data)
    db.add(row)
    await db.commit()
    await db.refresh(row)
    await _publish(device_id)
    return row


@router.put("/connections/{conn_id}", response_model=ConnectionOut)
async def update_connection(conn_id: int, body: ConnectionIn, db: AsyncSession = Depends(get_db)):
    row = await db.get(ConnectionProfile, conn_id)
    if row is None:
        raise HTTPException(404, "连接配置不存在")
    data = body.model_dump()
    data["node_id"] = body.node_id or row.node_id
    for k, v in data.items():
        setattr(row, k, v)
    await db.commit()
    await db.refresh(row)
    await _publish(row.device_id)
    return row


@router.delete("/connections/{conn_id}", status_code=204)
async def delete_connection(conn_id: int, db: AsyncSession = Depends(get_db)):
    row = await db.get(ConnectionProfile, conn_id)
    if row is None:
        raise HTTPException(404, "连接配置不存在")
    device_id = row.device_id
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
    await _publish(device_id)


async def _publish(device_id: int) -> None:
    if state.enabled:
        from ..cluster.sync import build_device_entry
        from ..events import bus
        entry = await build_device_entry(device_id)
        if entry:
            bus.publish("device_changed", entry)


@router.post("/connections/{conn_id}/open", response_model=SessionOut, status_code=201)
async def open_session(conn_id: int, db: AsyncSession = Depends(get_db)):
    profile = await db.get(ConnectionProfile, conn_id)
    if profile is None:
        raise HTTPException(404, "连接配置不存在")
    # 物理端口在远程节点 → 内联参数 RPC 到归属节点打开（对方库中未必有此配置行）
    if profile.node_id not in (self_node_id(), "local"):
        peer = state.peers.get(profile.node_id)
        if peer is None or peer.status != "online":
            raise HTTPException(503, f"归属节点 {profile.node_id} 不在线")
        try:
            r = await cluster_client.open_remote(
                peer, kind=profile.kind, params=profile.params, device_id=profile.device_id)
        except httpx.HTTPStatusError as exc:
            raise HTTPException(exc.response.status_code, f"归属节点返回错误: {exc.response.text}") from exc
        return SessionOut(
            id=r["session_id"], connection_id=conn_id, opened_by="admin",
            opened_at=datetime.now(timezone.utc), closed_at=None,
            status="connecting", last_error="", node_id=profile.node_id,
        )
    try:
        row = await manager.open(conn_id)
    except ConnectorError as exc:
        # detail 携带持有端口的会话 ID，前端可直接跳转已打开的终端
        raise HTTPException(409, detail={
            "message": str(exc), "session_id": exc.holder_session_id,
        }) from exc
    out = SessionOut.model_validate(row)
    out.node_id = self_node_id()
    return out
