"""集群管理 API（前端用）：节点列表、加入/移除、mDNS 待批准、远程代理、批量执行。"""
import logging

import httpx
from fastapi import APIRouter, Depends, HTTPException
from pydantic import BaseModel
from sqlalchemy import select

from ..auth import require_admin, require_operator
from ..batch_exec import batch_exec_local
from ..cluster import client
from ..cluster.state import state
from ..cluster.sync import attach_peer
from ..config import get_settings
from ..database import SessionLocal
from ..models import Node
from ..schemas import (BatchExecIn, BatchExecOut, DiscoveredNode, JoinIn,
                       NodeOut, ProxyDeviceOut, RemoteOpenIn, RemoteOpenOut)

log = logging.getLogger("linkhub.cluster.api")
router = APIRouter(prefix="/api/cluster", tags=["cluster"])


def _require_enabled() -> None:
    if not state.enabled:
        raise HTTPException(400, "集群模式未启用（设置 LINKHUB_CLUSTER_ENABLED=true 并配置令牌）")


def _peer_or_404(node_id: str):
    peer = state.peers.get(node_id)
    if peer is None:
        raise HTTPException(404, f"节点 {node_id} 不存在")
    return peer


# ---------- 节点管理 ----------
@router.get("/nodes", response_model=list[NodeOut])
async def list_nodes():
    async with SessionLocal() as db:
        rows = (await db.execute(select(Node))).scalars().all()
    out = []
    for r in rows:
        o = NodeOut.model_validate(r)
        peer = state.peers.get(r.node_id)
        if peer:  # 运行态覆盖
            o.status, o.resources = peer.status, peer.resources
        if r.is_self:
            o.status = "online"
        out.append(o)
    return out


@router.get("/info")
async def cluster_info():
    return {"enabled": state.enabled, "node_id": state.self_id,
            "name": state.self_name, "address": state.address}


@router.post("/join", response_model=NodeOut, status_code=201, dependencies=[Depends(require_admin)])
async def join(body: JoinIn):
    """加入集群：向对方发起握手，登记为 peer，并 gossip 尝试加入其已知节点。"""
    _require_enabled()
    address = body.address.rstrip("/")
    settings = get_settings()
    token = body.token or settings.cluster_token
    self_info = {"node_id": state.self_id, "name": state.self_name, "address": state.address}
    try:
        resp = await client.handshake(address, self_info, token=token)
    except httpx.HTTPStatusError as exc:
        raise HTTPException(403, f"握手被拒绝：{exc.response.status_code}，检查集群令牌") from exc
    except httpx.HTTPError as exc:
        raise HTTPException(502, f"无法连接 {address}: {exc}") from exc
    except ValueError as exc:
        # 系统代理等环境配置问题（如 socks:// scheme httpx 不支持）
        raise HTTPException(502, f"网络环境异常：{exc}（可尝试 unset HTTP_PROXY/HTTPS_PROXY 或联系管理员）") from exc

    if resp["node_id"] == state.self_id:
        raise HTTPException(400, "这是本节点自己")
    peer = await _upsert_peer(resp["node_id"], resp["name"], resp["address"])
    state.pending.pop(resp["node_id"], None)

    # gossip：尝试加入对方已知的其他节点（失败不阻塞）
    for p in resp.get("peers", []):
        if p["node_id"] not in (state.self_id, *state.peers.keys()):
            try:
                r2 = await client.handshake(p["address"], self_info, token=token)
                await _upsert_peer(r2["node_id"], r2["name"], r2["address"])
            except Exception as exc:
                log.info("gossip 加入 %s 失败: %s", p["address"], exc)
    return await _node_out(peer.node_id)


async def _upsert_peer(node_id: str, name: str, address: str):
    """注册/更新 peer。同地址换过身份的（重装丢库）自动剔除旧身份。"""
    from datetime import datetime, timezone
    address = address.rstrip("/")
    async with SessionLocal() as db:
        rows = (await db.execute(select(Node).where(Node.address == address))).scalars().all()
        for stale in rows:
            if stale.node_id != node_id and not stale.is_self:
                log.info("剔除同地址旧身份: %s（被 %s 替换）", stale.node_id, node_id)
                state.drop_peer(stale.node_id)
                await db.delete(stale)
        row = await db.get(Node, node_id)
        if row is None:
            row = Node(node_id=node_id)
            db.add(row)
        row.name, row.address, row.is_self = name, address, False
        row.status, row.last_seen = "online", datetime.now(timezone.utc)
        await db.commit()
    peer = state.register_peer(node_id, name, address)
    await attach_peer(peer)
    return peer


async def _node_out(node_id: str) -> NodeOut:
    async with SessionLocal() as db:
        row = await db.get(Node, node_id)
    return NodeOut.model_validate(row)


@router.post("/leave/{node_id}", status_code=204, dependencies=[Depends(require_admin)])
async def leave(node_id: str):
    _require_enabled()
    _peer_or_404(node_id)
    state.drop_peer(node_id)
    async with SessionLocal() as db:
        row = await db.get(Node, node_id)
        if row:
            await db.delete(row)
            await db.commit()


@router.get("/discovered", response_model=list[DiscoveredNode])
async def discovered():
    """mDNS/UDP 发现但尚未加入的节点。"""
    known = {state.self_id, *state.peers.keys(), *state.dismissed}
    return [DiscoveredNode(**e) for nid, e in state.pending.items() if nid not in known]


class _DismissBody(BaseModel):
    node_ids: list[str]


@router.post("/dismiss", status_code=204, dependencies=[Depends(require_admin)])
async def dismiss(body: _DismissBody):
    """忽略发现的节点：移出待批准且不再因 beacon/mDNS 重新出现。"""
    for nid in body.node_ids:
        state.pending.pop(nid, None)
        state.dismissed.add(nid)


# ---------- 远程代理 ----------
@router.get("/proxy/{node_id}/devices/{device_id}", response_model=ProxyDeviceOut)
async def proxy_device(node_id: str, device_id: int):
    peer = _peer_or_404(node_id)
    try:
        return await client.fetch_device(peer, device_id)
    except httpx.HTTPStatusError as exc:
        raise HTTPException(exc.response.status_code, f"归属节点返回错误: {exc.response.text}") from exc
    except httpx.HTTPError as exc:
        raise HTTPException(503, f"无法连接归属节点: {exc}") from exc


@router.post("/proxy/{node_id}/open", response_model=RemoteOpenOut, status_code=201, dependencies=[Depends(require_operator)])
async def proxy_open(node_id: str, body: RemoteOpenIn):
    peer = _peer_or_404(node_id)
    if peer.status != "online":
        raise HTTPException(503, f"节点 {peer.name} 不在线")
    try:
        r = await client.open_remote(peer, body.connection_id)
        return RemoteOpenOut(session_id=r["session_id"], node_id=node_id)
    except httpx.HTTPStatusError as exc:
        raise HTTPException(exc.response.status_code, f"归属节点返回错误: {exc.response.text}") from exc
    except httpx.HTTPError as exc:
        raise HTTPException(503, f"无法连接归属节点: {exc}") from exc


@router.post("/proxy/{node_id}/sessions/{session_id}/close")
async def proxy_close(node_id: str, session_id: int):
    peer = _peer_or_404(node_id)
    await client.close_remote(peer, session_id)
    return {"closed": True}


@router.delete("/proxy/{node_id}/devices/{device_id}", status_code=204, dependencies=[Depends(require_operator)])
async def proxy_delete_device(node_id: str, device_id: int):
    """剔除远程节点的设备（级联清理在归属节点完成）。"""
    peer = _peer_or_404(node_id)
    if peer.status != "online":
        raise HTTPException(503, f"节点 {peer.name} 不在线")
    try:
        await client.delete_remote_device(peer, device_id)
    except httpx.HTTPStatusError as exc:
        raise HTTPException(exc.response.status_code, "归属节点返回错误") from exc
    state.directory.pop((node_id, device_id), None)


class _ProxyFromTemplateIn(BaseModel):
    name: str
    param_overrides: dict = {}


@router.post("/proxy/{node_id}/from-template/{key}", status_code=201, dependencies=[Depends(require_operator)])
async def proxy_from_template(node_id: str, key: str, body: _ProxyFromTemplateIn):
    """在远程节点上用模板创建设备（设备归属该节点）。"""
    peer = _peer_or_404(node_id)
    if peer.status != "online":
        raise HTTPException(503, f"节点 {peer.name} 不在线")
    try:
        return await client.from_template_remote(peer, key, body.name, body.param_overrides)
    except httpx.HTTPStatusError as exc:
        raise HTTPException(exc.response.status_code, "归属节点返回错误") from exc


# ---------- 批量执行（C3） ----------
@router.post("/batch/exec", response_model=BatchExecOut, dependencies=[Depends(require_operator)])
async def batch_exec(body: BatchExecIn):
    """按归属节点分组：本机直接执行，远程节点 RPC 分发后合并。"""
    local_ids, by_peer = [], {}
    for t in body.targets:
        if t.node_id in ("local", state.self_id):
            local_ids.append(t.device_id)
        else:
            by_peer.setdefault(t.node_id, []).append(t.device_id)

    results = []
    if local_ids:
        results += await batch_exec_local(local_ids, body.command, body.wait_ms,
                                          node_id=state.self_id)
    for node_id, dev_ids in by_peer.items():
        peer = state.peers.get(node_id)
        if peer is None or peer.status != "online":
            results += [{"node_id": node_id, "device_id": d, "device_name": f"#{d}",
                         "ok": False, "output": "", "error": "节点不在线"} for d in dev_ids]
            continue
        try:
            results += await client.batch_remote(peer, dev_ids, body.command, body.wait_ms)
        except Exception as exc:
            results += [{"node_id": node_id, "device_id": d, "device_name": f"#{d}",
                         "ok": False, "output": "", "error": str(exc)} for d in dev_ids]
    return BatchExecOut(results=results)
