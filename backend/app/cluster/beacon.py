"""UDP 广播节点发现（比 mDNS 更皮实：单网段广播，不依赖组播路由）。

每 3s 向 255.255.255.255:DISCOVERY_PORT 广播本节点名片（不含令牌），
同网段节点收到后进"待批准"列表。beacon 只用于发现，加入仍需令牌握手。
"""
import asyncio
import json
import logging
import socket

from ..config import get_settings
from .state import state

log = logging.getLogger("linkhub.cluster.beacon")


def _broadcast_addrs() -> list[str]:
    """所有本地接口的定向广播地址 + 255.255.255.255。
    多网卡机器上 255.255.255.255 只走默认路由接口，可能到不了目标网段。"""
    addrs = {"255.255.255.255"}
    try:
        import fcntl
        import struct
        for _, ifname in socket.if_nameindex():
            try:
                s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
                brd = fcntl.ioctl(s.fileno(), 0x8919,  # SIOCGIFBRDADDR
                                  struct.pack("256s", ifname.encode()))[20:24]
                s.close()
                addrs.add(socket.inet_ntoa(brd))
            except OSError:
                continue
    except ImportError:  # 非 Linux（Windows 无 fcntl）
        pass
    return sorted(addrs)


class _Protocol(asyncio.DatagramProtocol):
    def datagram_received(self, data: bytes, addr) -> None:
        try:
            card = json.loads(data)
        except (json.JSONDecodeError, UnicodeDecodeError):
            return
        node_id = card.get("node_id")
        if (not node_id or node_id == state.self_id
                or node_id in state.peers or node_id in state.dismissed):
            return
        if not card.get("address"):
            return
        if state.pending.get(node_id) != card:
            state.pending[node_id] = card
            log.info("UDP 发现节点: %s (%s)", card.get("name"), card.get("address"))


class UdpBeacon:
    def __init__(self) -> None:
        self._send_task: asyncio.Task | None = None
        self._transport = None

    async def start(self) -> None:
        settings = get_settings()
        if not (state.enabled and settings.discovery_beacon):
            return
        port = settings.discovery_port
        try:
            loop = asyncio.get_running_loop()
            # 手动建 socket：SO_REUSEADDR/PORT 允许同机多节点共绑；SO_BROADCAST 收广播
            # （create_datagram_endpoint 的 reuse_address 参数在 3.12+ 已移除）
            sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
            sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            if hasattr(socket, "SO_REUSEPORT"):
                sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEPORT, 1)
            sock.setsockopt(socket.SOL_SOCKET, socket.SO_BROADCAST, 1)
            sock.bind(("0.0.0.0", port))
            sock.setblocking(False)
            self._transport, _ = await loop.create_datagram_endpoint(_Protocol, sock=sock)
            self._send_task = asyncio.create_task(self._broadcast_loop(port))
            log.info("UDP 发现已启动（端口 %d）", port)
        except Exception as exc:
            log.warning("UDP 广播发现启动失败: %r", exc)

    async def _broadcast_loop(self, port: int) -> None:
        sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        sock.setsockopt(socket.SOL_SOCKET, socket.SO_BROADCAST, 1)
        sock.setblocking(False)
        loop = asyncio.get_running_loop()
        card = json.dumps({"v": 1, "node_id": state.self_id, "name": state.self_name,
                           "address": state.address}, ensure_ascii=False).encode()
        targets = _broadcast_addrs()
        log.info("beacon 广播目标: %s", targets)
        while True:
            for target in targets:
                try:
                    await loop.sock_sendto(sock, card, (target, port))
                except OSError as exc:
                    log.debug("beacon 发送失败 %s: %s", target, exc)
            await asyncio.sleep(3)

    async def stop(self) -> None:
        if self._send_task:
            self._send_task.cancel()
        if self._transport:
            self._transport.close()


beacon = UdpBeacon()
