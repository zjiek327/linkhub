"""集群内部 API：仅节点间调用，X-LinkHub-Token 认证，不暴露给前端。"""
from datetime import datetime, timezone

from fastapi import APIRouter, Depends, HTTPException
from pydantic import BaseModel, Field
from sqlalchemy import select

from ..batch_exec import batch_exec_local
from ..cluster.auth import require_cluster_token
from ..cluster.state import state
from ..cluster.sync import attach_peer, build_local_directory
from ..connectors import ConnectorError, list_kinds
from ..database import SessionLocal
from ..models import ConnectionProfile, Device, Node
from ..schemas import HandshakeIn, RemoteOpenIn
from ..session_manager import manager

router = APIRouter(prefix="/api/cluster/internal",
                   tags=["cluster-internal"], dependencies=[Depends(require_cluster_token)])


@router.get("/ping")
async def ping():
    return {
        "node_id": state.self_id, "name": state.self_name, "address": state.address,
        "ts": datetime.now(timezone.utc).isoformat(),
        "resources": await _self_resources(),
    }


async def _self_resources() -> dict:
    async with SessionLocal() as db:
        row = await db.get(Node, state.self_id)
        return row.resources if row else {}


@router.post("/handshake")
async def handshake(body: HandshakeIn):
    """加入握手：登记对方为 peer，返回自身信息与已知 peer 列表（gossip）。"""
    if body.node_id == state.self_id:
        raise HTTPException(400, "不能加入自己")
    async with SessionLocal() as db:
        row = await db.get(Node, body.node_id)
        if row is None:
            row = Node(node_id=body.node_id)
            db.add(row)
        row.name, row.address = body.name, body.address.rstrip("/")
        row.status, row.is_self = "online", False
        row.last_seen = datetime.now(timezone.utc)
        await db.commit()
    peer = state.register_peer(body.node_id, body.name, body.address)
    await attach_peer(peer)
    return {
        "node_id": state.self_id, "name": state.self_name, "address": state.address,
        "peers": [
            {"node_id": p.node_id, "name": p.name, "address": p.address}
            for p in state.peers.values() if p.node_id != body.node_id
        ],
    }


@router.get("/directory")
async def directory():
    return await build_local_directory()


@router.get("/devices/{device_id}")
async def device_detail(device_id: int):
    async with SessionLocal() as db:
        d = await db.get(Device, device_id)
        if d is None:
            raise HTTPException(404, "设备不存在")
        conns = (await db.execute(select(ConnectionProfile).where(
            ConnectionProfile.device_id == device_id))).scalars().all()
    return {
        "device": {
            "id": d.id, "name": d.name, "description": d.description, "location": d.location,
            "owner": d.owner, "tags": d.tags or [], "group_id": d.group_id,
            "template_id": d.template_id, "node_id": d.node_id,
            "online": d.id in manager.online_device_ids(),
            "created_at": d.created_at.isoformat() if d.created_at else None,
            "updated_at": d.updated_at.isoformat() if d.updated_at else None,
        },
        "connections": [{
            "id": c.id, "device_id": c.device_id, "kind": c.kind, "name": c.name,
            "params": c.params, "node_id": c.node_id, "enabled": c.enabled,
        } for c in conns],
    }


class _OpenIn(BaseModel):
    connection_id: int | None = None
    kind: str | None = None       # 内联模式：对方节点存的配置指向本机端口
    params: dict | None = None
    device_id: int = 0


@router.post("/open")
async def open_session(body: _OpenIn):
    from ..cluster import client
    if body.connection_id is not None:
        async with SessionLocal() as db:
            profile = await db.get(ConnectionProfile, body.connection_id)
        if profile is None:
            raise HTTPException(404, "连接配置不存在")
        kind, params, device_id = profile.kind, profile.params, profile.device_id
    elif body.kind and body.params is not None:
        kind, params, device_id = body.kind, body.params, body.device_id
    else:
        raise HTTPException(400, "需要 connection_id 或 kind+params")
    try:
        row = await manager.open(body.connection_id) if body.connection_id is not None else (
            await manager.open_inline(kind, params, device_id))
    except ConnectorError as exc:
        raise HTTPException(409, str(exc)) from exc
    return {"session_id": row.id, "node_id": state.self_id}


@router.post("/sessions/{session_id}/close")
async def close_session(session_id: int):
    ok = await manager.close(session_id)
    if not ok:
        raise HTTPException(404, "会话不存在或已关闭")
    return {"closed": True}


class _BatchExecIn(BaseModel):
    device_ids: list[int]
    command: str = Field(min_length=1)
    wait_ms: int = Field(default=1500, ge=100, le=30000)


@router.post("/batch/exec")
async def batch_exec(body: _BatchExecIn):
    results = await batch_exec_local(body.device_ids, body.command, body.wait_ms,
                                     node_id=state.self_id)
    return {"results": results}


@router.get("/connector-kinds")
async def kinds():
    return list_kinds()
