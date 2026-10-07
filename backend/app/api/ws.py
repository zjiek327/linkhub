"""WebSocket 网关：终端数据流（本机直读 / 集群中继）+ 事件推送 + 集群内部 WS。"""
import asyncio
import contextlib
import json
import logging

import websockets
from fastapi import APIRouter, WebSocket, WebSocketDisconnect

from ..cluster.auth import check_ws_token
from ..cluster.client import ws_address
from ..cluster.state import state
from ..config import get_settings
from ..events import bus
from ..session_manager import manager

log = logging.getLogger("linkhub.ws")
router = APIRouter()


# ---------- 共享管道：本地会话 <-> 任一 WS 端（浏览器或 peer 中继） ----------
async def _pipe_session(ws: WebSocket, session_id: int) -> None:
    rt = manager.get(session_id)
    if rt is None:
        await ws.close(code=4404, reason="会话不存在或已关闭")
        return
    await ws.accept()
    q = manager.subscribe(session_id)
    local_echo = bool(rt.params.get("local_echo", False))

    async def downstream():  # 设备 → 对端
        while True:
            try:
                chunk = await asyncio.wait_for(q.get(), timeout=2)
                await ws.send_bytes(chunk)
            except TimeoutError:
                if manager.get(session_id) is None:  # 会话已终结，主动断开
                    await ws.close(code=1000, reason="会话已结束")
                    return

    async def upstream():  # 对端 → 设备
        while True:
            try:
                msg = await ws.receive()
            except Exception:  # 对端断开（含断开后重复 receive）
                return
            data = msg.get("bytes") or (msg.get("text") or "").encode()
            if not data:
                continue
            try:
                await manager.write(session_id, data)
            except Exception:
                return
            if local_echo:
                await ws.send_bytes(data)

    tasks = [asyncio.create_task(downstream()), asyncio.create_task(upstream())]
    try:
        await asyncio.wait(tasks, return_when=asyncio.FIRST_COMPLETED)
    except (WebSocketDisconnect, RuntimeError):
        pass
    finally:
        manager.unsubscribe(session_id, q)
        for t in tasks:
            t.cancel()
            # CancelledError 继承 BaseException，必须显式 suppress
            with contextlib.suppress(asyncio.CancelledError, Exception):
                await t


@router.websocket("/ws/terminal/{session_id}")
async def terminal(ws: WebSocket, session_id: int, node: str | None = None):
    """xterm.js ↔ 会话。node 参数非本机时为集群中继模式。"""
    if node and state.enabled and node != state.self_id:
        await _relay_terminal(ws, session_id, node)
        return
    if manager.get(session_id) is None and not state.enabled:
        # 单机模式直接 404；集群模式可能是远程会话忘了带 node
        await ws.close(code=4404, reason="会话不存在或已关闭")
        return
    await _pipe_session(ws, session_id)


async def _relay_terminal(ws: WebSocket, session_id: int, node_id: str) -> None:
    """浏览器↔本节点↔归属节点 的字节中继。"""
    peer = state.peers.get(node_id)
    if peer is None or peer.status != "online":
        await ws.close(code=4403, reason=f"节点 {node_id} 不在线")
        return
    await ws.accept()
    url = f"{ws_address(peer.address)}/ws/cluster/relay/{session_id}"
    headers = {"X-LinkHub-Token": get_settings().cluster_token,
               "X-LinkHub-Node-Id": state.self_id}
    try:
        async with websockets.connect(url, additional_headers=headers, ping_interval=15) as pws:
            async def browser_to_peer():
                while True:
                    msg = await ws.receive()
                    data = msg.get("bytes") or (msg.get("text") or "").encode()
                    if data:
                        await pws.send(data)

            async def peer_to_browser():
                async for chunk in pws:
                    await ws.send_bytes(chunk if isinstance(chunk, bytes) else chunk.encode())

            tasks = [asyncio.create_task(browser_to_peer()), asyncio.create_task(peer_to_browser())]
            try:
                await asyncio.wait(tasks, return_when=asyncio.FIRST_COMPLETED)
            finally:
                for t in tasks:
                    t.cancel()
                    with contextlib.suppress(asyncio.CancelledError, Exception):
                        await t
    except (WebSocketDisconnect, RuntimeError):
        pass
    except Exception as exc:
        log.warning("中继连接失败 session=%d node=%s: %s", session_id, node_id, exc)
        with contextlib.suppress(Exception):
            await ws.close(code=1011, reason=f"中继失败: {exc}")
    log.info("中继结束: session=%d node=%s", session_id, node_id)


# ---------- 集群内部 WS ----------
@router.websocket("/ws/cluster/relay/{session_id}")
async def cluster_relay(ws: WebSocket, session_id: int):
    """peer 发起的终端中继落点：就是一条本地会话管道。"""
    if not check_ws_token(ws):
        await ws.close(code=4403, reason="集群令牌无效")
        return
    await _pipe_session(ws, session_id)


@router.websocket("/ws/cluster/events")
async def cluster_events(ws: WebSocket):
    """peer 事件订阅：收到的事件标记来源后注入本地总线。"""
    if not check_ws_token(ws):
        await ws.close(code=4403, reason="集群令牌无效")
        return
    await ws.accept()
    peer_id = ws.headers.get("X-LinkHub-Node-Id", "unknown")
    try:
        while True:
            msg = json.loads(await ws.receive_text())
            bus.publish(msg["event"], msg.get("data", {}), origin=peer_id)
    except (WebSocketDisconnect, RuntimeError, json.JSONDecodeError):
        pass


# ---------- 前端事件推送 ----------
@router.websocket("/ws/events")
async def events(ws: WebSocket):
    """全局事件：session_status / node_status 等，驱动前端徽标。"""
    await ws.accept()
    q = bus.subscribe()
    try:
        while True:
            msg = await q.get()
            msg.pop("_origin", None)
            await ws.send_text(json.dumps(msg, ensure_ascii=False, default=str))
    except (WebSocketDisconnect, RuntimeError):
        pass
    finally:
        bus.unsubscribe(q)
