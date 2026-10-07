"""集群运行时状态：本机身份、peer 注册表、设备目录缓存、后台任务句柄。"""
import asyncio
import socket
import secrets
from dataclasses import dataclass, field
from datetime import datetime, timezone

from sqlalchemy import select

from ..config import get_settings
from ..database import SessionLocal
from ..models import Meta, Node

LOCAL = "local"


@dataclass
class PeerState:
    node_id: str
    name: str
    address: str
    status: str = "online"
    last_seen: float = 0.0
    resources: dict = field(default_factory=dict)
    heartbeat_task: asyncio.Task | None = None
    event_link_task: asyncio.Task | None = None


class ClusterState:
    def __init__(self) -> None:
        self.enabled = False
        self.self_id: str = LOCAL
        self.self_name: str = ""
        self.address: str = ""
        self.peers: dict[str, PeerState] = {}
        # 远程设备目录缓存: {(node_id, device_id): entry}
        self.directory: dict[tuple[str, int], dict] = {}
        # mDNS/UDP 发现待批准: {node_id: {node_id, name, address}}
        self.pending: dict[str, dict] = {}
        # 被用户忽略的发现节点（不再出现在待批准）
        self.dismissed: set[str] = set()
        self.background_tasks: list[asyncio.Task] = []
        self._started = False

    # ---------- 生命周期 ----------
    async def start(self) -> None:
        settings = get_settings()
        if not settings.cluster_enabled:
            return
        if not settings.cluster_token:
            import logging
            logging.getLogger("linkhub.cluster").error(
                "集群已启用但 LINKHUB_CLUSTER_TOKEN 为空，集群功能关闭")
            return
        self.enabled = True
        self.self_name = settings.node_name or socket.gethostname()
        self.address = settings.advertise_addr.rstrip("/")
        await self._ensure_identity()
        await self._load_peers()
        self._started = True
        # 重启后恢复 peer 的心跳/事件链路与目录同步（懒加载避免循环导入）
        from .sync import attach_peer
        for peer in list(self.peers.values()):
            await attach_peer(peer)

    async def stop(self) -> None:
        for p in self.peers.values():
            for t in (p.heartbeat_task, p.event_link_task):
                if t:
                    t.cancel()
        for t in self.background_tasks:
            t.cancel()
        self.peers.clear()
        self.directory.clear()
        self._started = False

    async def _ensure_identity(self) -> None:
        async with SessionLocal() as db:
            row = await db.get(Meta, "node_id")
            if row is None:
                row = Meta(key="node_id", value="n-" + secrets.token_hex(4))
                db.add(row)
            self.self_id = row.value
            self_row = await db.get(Node, self.self_id)
            if self_row is None:
                self_row = Node(node_id=self.self_id, is_self=True)
                db.add(self_row)
            self_row.name = self.self_name
            self_row.address = self.address
            self_row.status = "online"
            self_row.last_seen = datetime.now(timezone.utc)
            await db.commit()

    async def _load_peers(self) -> None:
        async with SessionLocal() as db:
            rows = (await db.execute(select(Node).where(Node.is_self.is_(False)))).scalars().all()
        for r in rows:
            self.register_peer(r.node_id, r.name, r.address, status="offline")

    # ---------- peer 注册表 ----------
    def register_peer(self, node_id: str, name: str, address: str, status: str = "online") -> PeerState:
        p = self.peers.get(node_id)
        if p is None:
            p = PeerState(node_id=node_id, name=name, address=address.rstrip("/"))
            self.peers[node_id] = p
        p.name, p.address, p.status = name, address.rstrip("/"), status
        return p

    def drop_peer(self, node_id: str) -> None:
        p = self.peers.pop(node_id, None)
        if p:
            for t in (p.heartbeat_task, p.event_link_task):
                if t:
                    t.cancel()
        for key in [k for k in self.directory if k[0] == node_id]:
            del self.directory[key]

    # ---------- 目录缓存 ----------
    def upsert_entry(self, entry: dict) -> None:
        self.directory[(entry["node_id"], entry["id"])] = entry

    def remove_entry(self, node_id: str, device_id: int) -> None:
        self.directory.pop((node_id, device_id), None)

    def replace_node_directory(self, node_id: str, entries: list[dict]) -> None:
        """反熵对账：用全量快照替换该节点的缓存切片。"""
        keep = {(node_id, e["id"]) for e in entries}
        for key in [k for k in self.directory if k[0] == node_id and k not in keep]:
            del self.directory[key]
        for e in entries:
            self.upsert_entry(e)

    def find_device_node(self, device_id: int) -> str | None:
        """在缓存里找远程设备的归属节点。"""
        for (nid, did) in self.directory:
            if did == device_id:
                return nid
        return None


state = ClusterState()


def self_node_id() -> str:
    """集群启用时返回本节点 ID，否则 'local'。"""
    return state.self_id if state.enabled else LOCAL
