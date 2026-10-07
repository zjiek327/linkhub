"""mDNS 自动发现（C2）：广播本节点，监听同网段其他节点进入待批准列表。

zeroconf 是同步库：其 sync API 会把协程投到当前线程的运行中 loop 上等待——
在 async lifespan 里直接调用会自死锁（EventLoopBlocked），所以全部经 to_thread 跑。
监听回调在 zeroconf 内部线程触发；pending 是普通 dict，读写都快，不加锁。
"""
import asyncio
import logging
import threading
import time

from zeroconf import ServiceInfo, ServiceListener, Zeroconf

from ..config import get_settings
from .state import state

log = logging.getLogger("linkhub.cluster.mdns")

SERVICE_TYPE = "_linkhub._tcp.local."


class _Listener(ServiceListener):
    def add_service(self, zc: Zeroconf, type_: str, name: str) -> None:
        # 在浏览器线程里同步 get_service_info 会阻塞组播包处理导致超时，
        # 必须丢到独立线程里取
        threading.Thread(target=self._fetch, args=(zc, type_, name), daemon=True).start()

    def _fetch(self, zc: Zeroconf, type_: str, name: str) -> None:
        # SRV/A 记录常先于 TXT 到达，属性为空时重试等 TXT
        info = None
        for _ in range(4):
            try:
                info = zc.get_service_info(type_, name, timeout=3000)
            except Exception:
                return
            if info and info.properties:
                break
            time.sleep(0.5)
        if not info:
            return
        props = {k.decode(): v.decode() for k, v in (info.properties or {}).items()}
        node_id = props.get("node_id")
        if not node_id or node_id == state.self_id:
            return
        entry = {"node_id": node_id, "name": props.get("name", node_id),
                 "address": props.get("addr", "")}
        if entry["address"]:
            state.pending[node_id] = entry
            log.info("mDNS 发现节点: %s (%s)", entry["name"], entry["address"])

    def remove_service(self, zc: Zeroconf, type_: str, name: str) -> None:
        for nid, e in list(state.pending.items()):
            if name.startswith(nid):
                state.pending.pop(nid, None)

    def update_service(self, zc: Zeroconf, type_: str, name: str) -> None:
        self.add_service(zc, type_, name)


class MdnsService:
    def __init__(self) -> None:
        self._zc: Zeroconf | None = None
        self._listener = _Listener()

    async def start(self) -> None:
        settings = get_settings()
        if not (state.enabled and settings.cluster_mdns):
            return
        try:
            await asyncio.to_thread(self._start_sync, settings)
        except Exception as exc:
            log.warning("mDNS 启动失败（可能是无组播环境）: %r", exc)
            self._zc = None

    def _start_sync(self, settings) -> None:
        zc = Zeroconf()
        port = int(settings.advertise_addr.rsplit(":", 1)[-1])
        info = ServiceInfo(
            SERVICE_TYPE,
            f"{state.self_id}.{SERVICE_TYPE}",
            addresses=None,  # 由 addr 属性携带广播地址，避免依赖 IP 解析
            port=port,
            properties={
                "node_id": state.self_id,
                "name": state.self_name,
                "addr": state.address,
            },
        )
        zc.register_service(info)
        zc.add_service_listener(SERVICE_TYPE, self._listener)
        self._zc = zc
        log.info("mDNS 已广播: %s (%s)", state.self_id, state.address)

    async def stop(self) -> None:
        if self._zc:
            await asyncio.to_thread(self._stop_sync)

    def _stop_sync(self) -> None:
        self._zc.unregister_all_services()
        self._zc.close()
        self._zc = None


mdns = MdnsService()
