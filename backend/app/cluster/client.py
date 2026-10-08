"""peer 间 HTTP RPC 客户端。"""
import httpx

from ..config import get_settings
from .state import PeerState


def _headers(token: str | None = None) -> dict[str, str]:
    return {"X-LinkHub-Token": token or get_settings().cluster_token}


def _client(timeout: float) -> httpx.AsyncClient:
    # 集群内网直连，不信任系统代理（SOCKS 代理会让 httpx 崩）
    return httpx.AsyncClient(timeout=timeout, trust_env=False)


def ws_address(http_address: str) -> str:
    if http_address.startswith("https://"):
        return "wss://" + http_address[len("https://"):]
    return "ws://" + http_address.removeprefix("http://")


async def ping(address: str) -> dict:
    async with _client(4) as c:
        r = await c.get(f"{address}/api/cluster/internal/ping", headers=_headers())
        r.raise_for_status()
        return r.json()


async def handshake(address: str, self_info: dict, token: str | None = None) -> dict:
    async with _client(6) as c:
        r = await c.post(f"{address}/api/cluster/internal/handshake",
                         json=self_info, headers=_headers(token))
        r.raise_for_status()
        return r.json()


async def fetch_directory(peer: PeerState) -> list[dict]:
    async with _client(8) as c:
        r = await c.get(f"{peer.address}/api/cluster/internal/directory", headers=_headers())
        r.raise_for_status()
        return r.json()


async def fetch_device(peer: PeerState, device_id: int) -> dict:
    async with _client(6) as c:
        r = await c.get(f"{peer.address}/api/cluster/internal/devices/{device_id}",
                        headers=_headers())
        r.raise_for_status()
        return r.json()


async def open_remote(peer: PeerState, connection_id: int | None = None,
                      kind: str | None = None, params: dict | None = None,
                      device_id: int = 0) -> dict:
    body: dict = {"device_id": device_id}
    if connection_id is not None:
        body["connection_id"] = connection_id
    else:
        body["kind"], body["params"] = kind, params
    async with _client(10) as c:
        r = await c.post(f"{peer.address}/api/cluster/internal/open",
                         json=body, headers=_headers())
        r.raise_for_status()
        return r.json()


async def close_remote(peer: PeerState, session_id: int) -> None:
    async with _client(6) as c:
        await c.post(f"{peer.address}/api/cluster/internal/sessions/{session_id}/close",
                     headers=_headers())


async def batch_remote(peer: PeerState, device_ids: list[int], command: str, wait_ms: int) -> list[dict]:
    async with _client(wait_ms / 1000 + 15) as c:
        r = await c.post(f"{peer.address}/api/cluster/internal/batch/exec",
                         json={"device_ids": device_ids, "command": command, "wait_ms": wait_ms},
                         headers=_headers())
        r.raise_for_status()
        return r.json()["results"]


async def delete_remote_device(peer: PeerState, device_id: int) -> None:
    async with _client(8) as c:
        r = await c.delete(f"{peer.address}/api/cluster/internal/devices/{device_id}",
                           headers=_headers())
        r.raise_for_status()


async def from_template_remote(peer: PeerState, key: str, name: str,
                               param_overrides: dict) -> dict:
    async with _client(10) as c:
        r = await c.post(f"{peer.address}/api/cluster/internal/devices/from-template/{key}",
                         json={"name": name, "param_overrides": param_overrides},
                         headers=_headers())
        r.raise_for_status()
        return r.json()
