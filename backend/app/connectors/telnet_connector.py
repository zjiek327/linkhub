"""Telnet 连接器（telnetlib3）：老旧网络设备/交换机。"""
from collections.abc import AsyncIterator
from typing import Any, ClassVar

from .base import ConnectorError, register

try:
    import telnetlib3
except ImportError:  # 环境未装时保持可注册但 open 报错
    telnetlib3 = None


@register
class TelnetConnector:
    kind: ClassVar[str] = "telnet"

    def __init__(self, params: dict[str, Any]) -> None:
        self.params = params
        self._reader = None
        self._writer = None

    @classmethod
    def schema(cls) -> dict[str, Any]:
        return {
            "type": "object",
            "required": ["host"],
            "properties": {
                "host": {"type": "string", "title": "主机", "default": ""},
                "port": {"type": "integer", "title": "端口", "default": 23},
                "connect_timeout": {"type": "integer", "title": "连接超时(秒)", "default": 10},
            },
        }

    async def open(self) -> None:
        if telnetlib3 is None:
            raise ConnectorError("未安装 telnetlib3（pip install telnetlib3）")
        try:
            self._reader, self._writer = await telnetlib3.open_connection(
                self.params["host"], port=int(self.params.get("port", 23)),
                connect_minwait=1.0)
        except Exception as exc:
            raise ConnectorError(f"Telnet 连接失败: {exc}") from exc

    async def close(self) -> None:
        if self._writer:
            self._writer.close()

    async def write(self, data: bytes) -> None:
        if not self._writer:
            raise ConnectorError("Telnet 未打开")
        self._writer.write(data.decode("utf-8", errors="replace"))

    async def read(self) -> AsyncIterator[bytes]:
        if not self._reader:
            raise ConnectorError("Telnet 未打开")
        try:
            while True:
                chunk = await self._reader.read(4096)
                if not chunk:
                    raise ConnectorError("Telnet 会话已断开")
                yield chunk.encode()
        except ConnectorError:
            raise
        except Exception as exc:
            raise ConnectorError(f"Telnet 读取异常: {exc}") from exc
