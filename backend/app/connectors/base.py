"""连接器插件框架：新协议实现 Connector 协议并 @register 即可接入。"""
from collections.abc import AsyncIterator
from typing import Any, ClassVar, Protocol, runtime_checkable


class ConnectorError(Exception):
    pass


@runtime_checkable
class Connector(Protocol):
    kind: ClassVar[str]

    async def open(self) -> None: ...
    async def close(self) -> None: ...
    async def write(self, data: bytes) -> None: ...
    def read(self) -> AsyncIterator[bytes]: ...

    @classmethod
    def schema(cls) -> dict[str, Any]:
        """连接参数的 JSON Schema，前端据此自动渲染表单。"""
        ...


_REGISTRY: dict[str, type] = {}


def register(cls: type) -> type:
    kind = getattr(cls, "kind", None)
    if not kind:
        raise ValueError(f"连接器 {cls.__name__} 缺少 kind 类属性")
    _REGISTRY[kind] = cls
    return cls


def create_connector(kind: str, params: dict[str, Any]) -> Connector:
    cls = _REGISTRY.get(kind)
    if cls is None:
        raise ConnectorError(f"不支持的连接方式: {kind}（可用: {sorted(_REGISTRY)}）")
    return cls(params)


def list_kinds() -> list[dict[str, Any]]:
    return [{"kind": k, "schema": cls.schema()} for k, cls in sorted(_REGISTRY.items())]


# 导入即注册内置连接器
from . import serial_connector as _serial  # noqa: E402,F401
