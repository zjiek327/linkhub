"""peer 间事件链路：每 peer 一条全双工 WS，收发双方事件。

说明：A→B 与 B→A 会各建一条链路，事件会重复到达，但所有处理器都是幂等的
（目录 upsert / 状态置位），且 _origin 标记阻止二次转发，不会成环。
"""
import asyncio
import json
import logging

import websockets

from ..config import get_settings
from ..events import bus
from .client import ws_address
from .state import PeerState, state

log = logging.getLogger("linkhub.cluster.events")

# 允许跨节点转发的事件类型
FORWARDED = {"device_changed", "device_deleted", "session_status", "resource_update", "node_status"}


def _headers() -> dict:
    return {
        "X-LinkHub-Token": get_settings().cluster_token,
        "X-LinkHub-Node-Id": state.self_id,
    }


async def run_event_link(peer: PeerState) -> None:
    """与指定 peer 维持事件长连接，断线自动重连。"""
    url = f"{ws_address(peer.address)}/ws/cluster/events"
    while True:
        try:
            async with websockets.connect(url, additional_headers=_headers(), ping_interval=10) as ws:
                log.info("事件链路已建立: %s (%s)", peer.name, peer.address)
                forwarder = asyncio.create_task(_forward_local(ws))
                try:
                    async for raw in ws:
                        msg = json.loads(raw)
                        bus.publish(msg["event"], msg.get("data", {}), origin=peer.node_id)
                finally:
                    forwarder.cancel()
        except asyncio.CancelledError:
            raise
        except Exception as exc:
            log.debug("事件链路断开 %s: %s，3s 后重试", peer.address, exc)
        await asyncio.sleep(3)


async def _forward_local(ws) -> None:
    """把本机产生的事件发给对端（_origin 非空的是转发来的，跳过防环）。"""
    q = bus.subscribe()
    try:
        while True:
            msg = await q.get()
            if msg.get("_origin") is not None or msg["event"] not in FORWARDED:
                continue
            await ws.send(json.dumps(
                {"event": msg["event"], "data": msg["data"]},
                ensure_ascii=False, default=str,
            ))
    finally:
        bus.unsubscribe(q)
