"""MQTT 连接器（aiomqtt）：IoT 设备/网关接入。read=订阅主题流，write=发布。"""
import asyncio
from collections.abc import AsyncIterator
from typing import Any, ClassVar

from .base import ConnectorError, register

try:
    from aiomqtt import Client, Message
except ImportError:
    Client = None


@register
class MqttConnector:
    kind: ClassVar[str] = "mqtt"

    def __init__(self, params: dict[str, Any]) -> None:
        self.params = params
        self._client = None
        self._queue: asyncio.Queue[bytes] = asyncio.Queue(maxsize=1024)
        self._task: asyncio.Task | None = None

    @classmethod
    def schema(cls) -> dict[str, Any]:
        return {
            "type": "object",
            "required": ["host"],
            "properties": {
                "host": {"type": "string", "title": "Broker 主机", "default": ""},
                "port": {"type": "integer", "title": "端口", "default": 1883},
                "username": {"type": "string", "title": "用户名", "default": ""},
                "password": {"type": "string", "title": "密码", "default": ""},
                "topic_sub": {"type": "string", "title": "订阅主题", "default": "#",
                              "description": "终端里显示这些主题的消息"},
                "topic_pub": {"type": "string", "title": "发布主题", "default": "cmd",
                              "description": "终端里输入的行发布到该主题"},
            },
        }

    async def open(self) -> None:
        if Client is None:
            raise ConnectorError("未安装 aiomqtt（pip install aiomqtt）")
        try:
            self._client = Client(
                self.params["host"], port=int(self.params.get("port", 1883)),
                username=self.params.get("username") or None,
                password=self.params.get("password") or None)
            await self._client.__aenter__()
            await self._client.subscribe(self.params.get("topic_sub", "#"))
            self._task = asyncio.create_task(self._pump())
        except Exception as exc:
            raise ConnectorError(f"MQTT 连接失败: {exc}") from exc

    async def _pump(self) -> None:
        try:
            async for msg in self._client.messages:
                payload: bytes = bytes(msg.payload)
                line = f"[{msg.topic.value}] ".encode() + payload + b"\r\n"
                if not self._queue.full():
                    self._queue.put_nowait(line)
        except Exception as exc:
            if not self._queue.full():
                self._queue.put_nowait(f"\r\n[MQTT 断开: {exc}]\r\n".encode())

    async def close(self) -> None:
        if self._task:
            self._task.cancel()
        if self._client:
            try:
                await self._client.__aexit__(None, None, None)
            except Exception:
                pass

    async def write(self, data: bytes) -> None:
        if not self._client:
            raise ConnectorError("MQTT 未打开")
        # 按行发布：终端输入回车即发布一行
        for line in data.decode("utf-8", "replace").splitlines():
            if line.strip():
                await self._client.publish(self.params.get("topic_pub", "cmd"),
                                           payload=line.strip())

    async def read(self) -> AsyncIterator[bytes]:
        while True:
            item = await self._queue.get()
            if isinstance(item, bytes):
                yield item
            else:
                raise ConnectorError(str(item))
