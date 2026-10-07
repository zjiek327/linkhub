"""BLE 连接器（bleak）：蓝牙低功耗设备，notify 特征流。"""
import asyncio
from collections.abc import AsyncIterator
from typing import Any, ClassVar

from .base import ConnectorError, register

try:
    from bleak import BleakClient, BleakScanner
except ImportError:
    BleakClient = BleakScanner = None


@register
class BleConnector:
    kind: ClassVar[str] = "ble"

    def __init__(self, params: dict[str, Any]) -> None:
        self.params = params
        self._client = None
        self._queue: asyncio.Queue[bytes] = asyncio.Queue(maxsize=512)
        self._address = params.get("address", "")

    @classmethod
    def schema(cls) -> dict[str, Any]:
        return {
            "type": "object",
            "required": ["address"],
            "properties": {
                "address": {"type": "string", "title": "设备地址", "default": "",
                            "description": "MAC（Linux）或 UUID（macOS）；用发现接口扫描"},
                "char_notify": {"type": "string", "title": "Notify 特征 UUID", "default": "",
                                "description": "留空=自动选第一个 notify 特征"},
                "char_write": {"type": "string", "title": "Write 特征 UUID", "default": "",
                               "description": "留空=自动选第一个 writable 特征"},
                "timeout": {"type": "integer", "title": "扫描超时(秒)", "default": 10},
            },
        }

    async def open(self) -> None:
        if BleakClient is None:
            raise ConnectorError("未安装 bleak（pip install bleak）")
        if not self._address:
            raise ConnectorError("BLE 未配置 address")
        try:
            self._client = BleakClient(self._address)
            await asyncio.wait_for(self._client.connect(),
                                   timeout=int(self.params.get("timeout", 10)))
            char_notify = self.params.get("char_notify") or None
            if not char_notify:
                for s in self._client.services:
                    for c in s.characteristics:
                        if "notify" in c.properties:
                            char_notify = c.uuid
                            break
            if not char_notify:
                raise ConnectorError("未找到可订阅的 notify 特征")
            await self._client.start_notify(char_notify, self._on_notify)
        except ConnectorError:
            raise
        except Exception as exc:
            raise ConnectorError(f"BLE 连接 {self._address} 失败: {exc}") from exc

    def _on_notify(self, _handle: int, data: bytearray) -> None:
        if not self._queue.full():
            self._queue.put_nowait(bytes(data) + b"\r\n")

    async def close(self) -> None:
        if self._client:
            try:
                await self._client.disconnect()
            except Exception:
                pass

    async def write(self, data: bytes) -> None:
        if not self._client:
            raise ConnectorError("BLE 未打开")
        char_write = self.params.get("char_write") or None
        if not char_write:
            for s in self._client.services:
                for c in s.characteristics:
                    if "write" in c.properties or "write-without-response" in c.properties:
                        char_write = c.uuid
                        break
        if not char_write:
            raise ConnectorError("未找到可写特征")
        await self._client.write_gatt_char(char_write, data.rstrip(b"\r\n"))

    async def read(self) -> AsyncIterator[bytes]:
        while True:
            yield await self._queue.get()


async def scan_ble(timeout: float = 8.0) -> list[dict]:
    """BLE 扫描发现（P3 网络发现接口用）。"""
    if BleakScanner is None:
        raise ConnectorError("未安装 bleak")
    devices = await BleakScanner.discover(timeout=timeout)
    return [{"address": d.address, "name": d.name or "(未知)"} for d in devices]
