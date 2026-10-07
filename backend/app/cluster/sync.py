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
            # 同地址但 node_id 变了 → 对方重装了身份，自动替换幽灵节点
            if info.get("node_id") and info["node_id"] != peer.node_id:
                log.info("节点 %s 身份变更 %s → %s，自动替换",
                         peer.address, peer.node_id, info["node_id"])
                asyncio.create_task(replace_identity(peer, info))
                return
            peer.last_seen = asyncio.get_running_loop().time()
            peer.resources = info.get("resources", {})
            if peer.status != "online":
                await _set_peer_status(peer, "online")
                await attach_peer_directory(peer)
        except Exception:
            if peer.status == "online":
                await _set_peer_status(peer, "offline")
        await asyncio.sleep(settings.heartbeat_interval)


async def replace_identity(old: PeerState, info: dict) -> None:
    """旧身份 → 新身份：清理旧 peer 的目录缓存与数据库行，注册新 peer。"""
    from ..models import Node
    state.drop_peer(old.node_id)  # 心跳任务已返回，这里只清理事件链路与缓存
    async with SessionLocal() as db:
        row = await db.get(Node, old.node_id)
        if row:
            await db.delete(row)
        new_row = await db.get(Node, info["node_id"])
        if new_row is None:
            new_row = Node(node_id=info["node_id"])
            db.add(new_row)
        new_row.name = info.get("name", "")
        new_row.address = old.address
        new_row.status = "online"
        new_row.is_self = False
        new_row.last_seen = datetime.now(timezone.utc)
        await db.commit()
    bus.publish("node_status", {"node_id": old.node_id, "status": "offline", "replaced": True})
    peer = state.register_peer(info["node_id"], info.get("name", ""), old.address)
    await attach_peer(peer)


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


# ---------- 定向 peer 级联（广播被网络过滤时的兜底） ----------
async def direct_peers_loop() -> None:
    """对配置的 LINKHUB_CLUSTER_PEERS 地址周期性 ping：
    已是 peer 则跳过；新节点则自动握手加入。"""
    from . import client as cluster_client
    settings = get_settings()
    while True:
        await asyncio.sleep(settings.heartbeat_interval)
        for address in settings.peers_list():
            addr = address.rstrip("/")
            if any(p.address == addr for p in state.peers.values()):
                continue
            try:
                info = await cluster_client.ping(addr)
            except Exception:
                continue
            if info.get("node_id") in (state.self_id, None):
                continue
            log.info("定向发现节点 %s (%s)，执行握手", info.get("name"), addr)
            try:
                resp = await cluster_client.handshake(addr, {
                    "node_id": state.self_id, "name": state.self_name,
                    "address": state.address,
                })
                await _register_peer_row(resp["node_id"], resp["name"], resp["address"])
            except Exception as exc:
                log.warning("定向握手失败 %s: %s", addr, exc)


async def _register_peer_row(node_id: str, name: str, address: str) -> None:
    from ..models import Node
    async with SessionLocal() as db:
        row = await db.get(Node, node_id)
        if row is None:
            row = Node(node_id=node_id)
            db.add(row)
        row.name, row.address, row.is_self = name, address.rstrip("/"), False
        row.status = "online"
        row.last_seen = datetime.now(timezone.utc)
        await db.commit()
    peer = state.register_peer(node_id, name, address)
    await attach_peer(peer)


# ---------- 服务启停 ----------
def start_background_tasks() -> None:
    state.background_tasks = [
        asyncio.create_task(directory_sync_loop()),
        asyncio.create_task(resource_watch_loop()),
        asyncio.create_task(cache_sync_loop()),
        asyncio.create_task(direct_peers_loop()),
    ]
