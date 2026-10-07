"""进程内事件总线：设备/会话状态变更推送给前端。"""
import asyncio
from typing import Any


class EventBus:
    def __init__(self) -> None:
        self._subscribers: set[asyncio.Queue] = set()

    def subscribe(self) -> asyncio.Queue:
        q: asyncio.Queue = asyncio.Queue(maxsize=256)
        self._subscribers.add(q)
        return q

    def unsubscribe(self, q: asyncio.Queue) -> None:
        self._subscribers.discard(q)

    def publish(self, event: str, data: dict[str, Any]) -> None:
        msg = {"event": event, "data": data}
        for q in list(self._subscribers):
            if q.full():
                continue  # 慢消费者丢事件，不阻塞总线
            q.put_nowait(msg)


bus = EventBus()
