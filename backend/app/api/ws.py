"""WebSocket 网关：终端数据流 + 全局事件推送。"""
import asyncio
import json
import logging

from fastapi import APIRouter, WebSocket, WebSocketDisconnect

from ..events import bus
from ..session_manager import manager

log = logging.getLogger("linkhub.ws")
router = APIRouter()


@router.websocket("/ws/terminal/{session_id}")
async def terminal(ws: WebSocket, session_id: int):
    """xterm.js ↔ 会话 双向流。二进制帧 = 终端数据。"""
    rt = manager.get(session_id)
    if rt is None:
        await ws.close(code=4404, reason="会话不存在或已关闭")
        return
    await ws.accept()
    q = manager.subscribe(session_id)
    local_echo = bool(rt.params.get("local_echo", False))

    async def downstream():  # 设备 → 浏览器
        while True:
            chunk = await q.get()
            await ws.send_bytes(chunk)

    async def upstream():  # 浏览器 → 设备
        while True:
            msg = await ws.receive()
            data = msg.get("bytes") or (msg.get("text") or "").encode()
            if not data:
                continue
            await manager.write(session_id, data)
            if local_echo:
                await ws.send_bytes(data)

    tasks = [asyncio.create_task(downstream()), asyncio.create_task(upstream())]
    try:
        done, pending = await asyncio.wait(tasks, return_when=asyncio.FIRST_COMPLETED)
        for t in pending:
            t.cancel()
    except (WebSocketDisconnect, RuntimeError):
        pass
    finally:
        manager.unsubscribe(session_id, q)
        for t in tasks:
            t.cancel()
        log.info("终端断开: session=%d", session_id)


@router.websocket("/ws/events")
async def events(ws: WebSocket):
    """全局事件：session_status 等，驱动前端在线状态徽标。"""
    await ws.accept()
    q = bus.subscribe()
    try:
        while True:
            msg = await q.get()
            await ws.send_text(json.dumps(msg, ensure_ascii=False))
    except (WebSocketDisconnect, RuntimeError):
        pass
    finally:
        bus.unsubscribe(q)
