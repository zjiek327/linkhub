"""连接器包：导入 base 即完成内置连接器注册。"""
from .base import Connector, ConnectorError, create_connector, list_kinds, register

__all__ = ["Connector", "ConnectorError", "create_connector", "list_kinds", "register"]
