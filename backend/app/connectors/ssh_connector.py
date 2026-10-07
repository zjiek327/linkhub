"""SSH 连接器（asyncssh）：网络设备接入，支持密码/私钥凭证引用。"""
import asyncio
from collections.abc import AsyncIterator
from typing import Any, ClassVar

import asyncssh

from ..database import SessionLocal
from ..models import Credential
from ..services.crypto import decrypt
from .base import ConnectorError, register


async def _resolve_secret(credential_id: int | None) -> tuple[str, str]:
    """凭证引用 → (auth_kind, secret)。无凭证时返回 ("password", "")。"""
    if not credential_id:
        return "password", ""
    async with SessionLocal() as db:
        cred = await db.get(Credential, credential_id)
    if cred is None:
        raise ConnectorError(f"凭证 {credential_id} 不存在")
    return cred.type, decrypt(cred.secret_enc)


@register
class SshConnector:
    kind: ClassVar[str] = "ssh"

    def __init__(self, params: dict[str, Any]) -> None:
        self.params = params
        self._conn: asyncssh.SSHClientConnection | None = None
        self._chan: asyncssh.SSHClientChannel | None = None
        self._pending_cred_id = params.get("credential_id")

    @classmethod
    def schema(cls) -> dict[str, Any]:
        return {
            "type": "object",
            "required": ["host"],
            "properties": {
                "host": {"type": "string", "title": "主机", "default": "",
                         "description": "设备 IP 或主机名"},
                "port": {"type": "integer", "title": "端口", "default": 22},
                "username": {"type": "string", "title": "用户名", "default": "root"},
                "credential_id": {"type": "integer", "title": "凭证 ID", "default": 0,
                                  "description": "凭证管理中创建的密码/私钥，0=使用下方密码框"},
                "password": {"type": "string", "title": "密码", "default": "",
                             "description": "未配置凭证时直接填写"},
                "known_hosts": {"type": "string", "title": "主机指纹校验", "default": "auto",
                                "description": "auto=首次自动信任并记录 / 严格模式填 ssh-keygen 格式指纹"},
            },
        }

    async def open(self) -> None:
        host = self.params.get("host")
        if not host:
            raise ConnectorError("SSH 未配置 host")
        auth_kind, secret = await _resolve_secret(self._pending_cred_id or None)
        password = self.params.get("password", "") if auth_kind == "password" else ""
        client_key = secret if auth_kind == "key" else None
        if auth_kind == "password" and not password and secret:
            password = secret
        strict = self.params.get("known_hosts", "auto") not in ("", "auto")
        try:
            self._conn = await asyncssh.connect(
                host, port=int(self.params.get("port", 22)),
                username=self.params.get("username", "root"),
                password=password or None, client_keys=client_key,
                known_hosts=None if not strict else self.params.get("known_hosts", ""),
                keepalive_interval=15,
            )
        except (OSError, asyncssh.Error) as exc:
            raise ConnectorError(f"SSH 连接 {host} 失败: {exc}") from exc
        self._chan, _, _ = await self._conn.open_session(term_type="xterm-256color")
        self._chan.set_encoding("utf-8", errors="replace")

    async def close(self) -> None:
        if self._chan:
            self._chan.close()
        if self._conn:
            self._conn.close()

    async def write(self, data: bytes) -> None:
        if not self._chan:
            raise ConnectorError("SSH 未打开")
        self._chan.write(data.decode("utf-8", errors="replace"))

    async def read(self) -> AsyncIterator[bytes]:
        if not self._chan:
            raise ConnectorError("SSH 未打开")
        try:
            while True:
                chunk = await self._chan.read(4096)
                if not chunk:
                    raise ConnectorError("SSH 会话已断开")
                yield chunk.encode()
        except ConnectorError:
            raise
        except Exception as exc:
            raise ConnectorError(f"SSH 读取异常: {exc}") from exc
