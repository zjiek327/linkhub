"""集群内部端点认证：X-LinkHub-Token 校验。"""
from fastapi import HTTPException, Request, WebSocket

from ..config import get_settings


def _check(token: str | None) -> bool:
    settings = get_settings()
    return settings.cluster_enabled and settings.cluster_token and token == settings.cluster_token


async def require_cluster_token(request: Request) -> None:
    if not get_settings().cluster_enabled:
        raise HTTPException(404, "集群模式未启用")
    if not _check(request.headers.get("X-LinkHub-Token")):
        raise HTTPException(403, "集群令牌无效")


def check_ws_token(ws: WebSocket) -> bool:
    return _check(ws.headers.get("X-LinkHub-Token"))
