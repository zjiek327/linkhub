"""串口/USB 连接器（pyserial-asyncio）。"""
import asyncio
from collections.abc import AsyncIterator
from typing import Any, ClassVar

import serial_asyncio

from .base import ConnectorError, register

_PARITY_MAP = {"N": "N", "E": "E", "O": "O", "M": "M", "S": "S"}


@register
class SerialConnector:
    kind: ClassVar[str] = "serial"

    def __init__(self, params: dict[str, Any]) -> None:
        self.params = params
        self._reader: asyncio.StreamReader | None = None
        self._writer: asyncio.StreamWriter | None = None

    @classmethod
    def schema(cls) -> dict[str, Any]:
        return {
            "type": "object",
            "required": ["port"],
            "properties": {
                "port": {"type": "string", "title": "串口设备", "default": "/dev/ttyUSB0",
                         "description": "如 /dev/ttyUSB0、/dev/ttyACM0、COM3"},
                "baudrate": {"type": "integer", "title": "波特率", "default": 115200},
                "bytesize": {"type": "integer", "title": "数据位", "default": 8, "enum": [5, 6, 7, 8]},
                "parity": {"type": "string", "title": "校验", "default": "N", "enum": ["N", "E", "O", "M", "S"]},
                "stopbits": {"type": "integer", "title": "停止位", "default": 1, "enum": [1, 2]},
                "local_echo": {"type": "boolean", "title": "本地回显", "default": False,
                               "description": "对无回显设备（如裸串口）开启"},
            },
        }

    async def open(self) -> None:
        try:
            self._reader, self._writer = await serial_asyncio.open_serial_connection(
                url=self.params["port"],
                baudrate=int(self.params.get("baudrate", 115200)),
                bytesize=int(self.params.get("bytesize", 8)),
                parity=_PARITY_MAP.get(str(self.params.get("parity", "N")).upper(), "N"),
                stopbits=float(self.params.get("stopbits", 1)),
                timeout=0,          # 非阻塞读，由 read() 自行驱动
                write_timeout=5,
            )
        except Exception as exc:  # SerialException / FileNotFoundError / PermissionError
            raise ConnectorError(f"打开串口 {self.params.get('port')} 失败: {exc}") from exc

    async def close(self) -> None:
        if self._writer is not None:
            self._writer.close()
            try:
                await self._writer.wait_closed()
            except Exception:
                pass
        self._reader = self._writer = None

    async def write(self, data: bytes) -> None:
        if self._writer is None:
            raise ConnectorError("串口未打开")
        self._writer.write(data)
        await self._writer.drain()

    async def read(self) -> AsyncIterator[bytes]:
        if self._reader is None:
            raise ConnectorError("串口未打开")
        try:
            while True:
                chunk = await self._reader.read(4096)
                if not chunk:  # EOF：设备被拔出
                    raise ConnectorError("串口连接中断（设备可能被拔出）")
                yield chunk
        except ConnectorError:
            raise
        except Exception as exc:
            raise ConnectorError(f"串口读取异常: {exc}") from exc
