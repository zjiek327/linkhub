"""集群同步：目录构建、心跳、反熵对账、串口资源热插拔监视、事件驱动的缓存更新。"""
import asyncio
import logging
from datetime import datetime, timezone

from sqlalchemy import select
from serial.tools import list_ports

from ..config import get_settings
from ..database import SessionLocal
from ..events import bus
from ..models import ConnectionProfile, Device, Node
from ..session_manager import manager
from . import client
from .state import PeerState, state

log = logging.getLogger("linkhub.cluster.sync")


# ---------- 本机目录 ----------
async def build_local_directory() -> list[dict]:
    """本机设备的完整摘要列表，供 peer 拉取。"""
    async with SessionLocal() as db:
        devices = (await db.execute(select(Device))).scalars().all()
        conns = (await db.execute(select(ConnectionProfile))).scalars().all()
    by_device: dict[int, list] = {}
    for c in conns:
        by_device.setdefault(c.device_id, []).append({
            "id": c.id, "kind": c.kind, "name": c.name,
            "node_id": c.node_id, "enabled": c.enabled,
        })
    online = manager.online_device_ids()
    return [{
        "node_id": state.self_id,
        "id": d.id, "name": d.name, "description": d.description,
        "location": d.location, "owner": d.owner, "tags": d.tags or [],
        "group_id": d.group_id, "template_id": d.template_id,
        "online": d.id in online,
        "created_at": d.created_at.isoformat() if d.created_at else None,
        "updated_at": d.updated_at.isoformat() if d.updated_at else None,
        "connections": by_device.get(d.id, []),
    } for d in devices]


async def build_device_entry(device_id: int) -> dict | None:
    for e in await build_local_directory():
        if e["id"] == device_id:
            return e
    return None


# ---------- peer 生命周期 ----------
async def attach_peer(peer: PeerState) -> None:
    """启动心跳与事件链路，并做首次目录全量拉取。"""
    if peer.heartbeat_task is None or peer.heartbeat_task.done():
        peer.heartbeat_task = asyncio.create_task(_heartbeat_loop(peer))
    if peer.event_link_task is None or peer.event_link_task.done():
        from .events_link import run_event_link
        peer.event_link_task = asyncio.create_task(run_event_link(peer))
    try:
        entries = await client.fetch_directory(peer)
        state.replace_node_directory(peer.node_id, entries)
        await _set_peer_status(peer, "online")
    except Exception as exc:
        log.warning("首次目录拉取失败 %s: %s", peer.address, exc)
        await _set_peer_status(peer, "offline")


async def _heartbeat_loop(peer: PeerState) -> None:
    settings = get_settings()
    while True:
        try:
            info = await client.ping(peer.address)
            peer.last_seen = asyncio.get_running_loop().time()
            peer.resources = info.get("resources", {})
            if peer.status != "online":
                await _set_peer_status(peer, "online")
                await attach_peer_directory(peer)
        except Exception:
            if peer.status == "online":
                await _set_peer_status(peer, "offline")
        await asyncio.sleep(settings.heartbeat_interval)


async def attach_peer_directory(peer: PeerState) -> None:
    """节点恢复在线后重新对账。"""
    try:
        entries = await client.fetch_directory(peer)
        state.replace_node_directory(peer.node_id, entries)
    except Exception as exc:
        log.warning("目录对账失败 %s: %s", peer.address, exc)


async def _set_peer_status(peer: PeerState, status: str) -> None:
    peer.status = status
    async with SessionLocal() as db:
        row = await db.get(Node, peer.node_id)
        if row:
            row.status = status
            row.last_seen = datetime.now(timezone.utc)
            await db.commit()
    bus.publish("node_status", {
        "node_id": peer.node_id, "name": peer.name, "status": status,
    })


# ---------- 反熵对账（C3） ----------
async def directory_sync_loop() -> None:
    settings = get_settings()
    while True:
        await asyncio.sleep(settings.directory_sync_interval)
        for peer in list(state.peers.values()):
            if peer.status == "online":
                await attach_peer_directory(peer)


# ---------- 串口资源热插拔监视（C2） ----------
def _ports_snapshot() -> list[dict]:
    return sorted(
        {"device": p.device, "description": p.description or ""}
        for p in list_ports.comports()
        if "USB" in (p.hwid or "").upper() or "ttyACM" in p.device
        or (p.description and p.description != "n/a")
    ) or []


async def resource_watch_loop() -> None:
    settings = get_settings()
    last = _ports_snapshot()
    await _publish_self_resources(last)
    while True:
        await asyncio.sleep(settings.resource_watch_interval)
        current = _ports_snapshot()
        if [p["device"] for p in current] != [p["device"] for p in last]:
            last = current
            log.info("串口资源变化: %s", [p["device"] for p in current])
            await _publish_self_resources(current)
            bus.publish("resource_update", {"node_id": state.self_id, "serial_ports": current})


async def _publish_self_resources(ports: list[dict]) -> None:
    async with SessionLocal() as db:
        row = await db.get(Node, state.self_id)
        if row:
            row.resources = {"serial_ports": ports}
            await db.commit()


# ---------- 事件 → 目录缓存（本地订阅，处理 peer 转发来的事件） ----------
async def cache_sync_loop() -> None:
    q = bus.subscribe()
    try:
        while True:
            msg = await q.get()
            origin, event, data = msg.get("_origin"), msg.get("event"), msg.get("data", {})
            if origin is None:  # 本机事件不影响远程缓存
                continue
            if event == "device_changed":
                state.upsert_entry(data)
            elif event == "device_deleted":
                state.remove_entry(origin, data.get("device_id"))
            elif event == "session_status":
                entry = state.directory.get((origin, data.get("device_id")))
                if entry is not None and "device_online" in data:
                    entry["online"] = data["device_online"]
            elif event == "resource_update":
                peer = state.peers.get(origin)
                if peer:
                    peer.resources = {"serial_ports": data.get("serial_ports", [])}
    finally:
        bus.unsubscribe(q)


# ---------- 服务启停 ----------
def start_background_tasks() -> None:
    state.background_tasks = [
        asyncio.create_task(directory_sync_loop()),
        asyncio.create_task(resource_watch_loop()),
        asyncio.create_task(cache_sync_loop()),
    ]
