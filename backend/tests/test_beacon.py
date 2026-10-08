"""UDP 广播发现（beacon）与忽略列表测试。

广播地址在单机回环上可达（Linux 默认路由接口即收）；若环境禁广播则跳过。
"""
import asyncio
import json
import socket

import os

import pytest

pytestmark = pytest.mark.skipif(os.name != 'posix', reason='需要 POSIX pty')


from app.cluster.state import state


@pytest.mark.asyncio
async def test_udp_beacon_discovery():
    from app.cluster.beacon import UdpBeacon

    state.enabled = True
    state.self_id = "n-self-test"
    state.self_name = "本机测试"
    state.address = "http://127.0.0.1:9000"
    state.pending.clear()
    state.dismissed.clear()

    # 模拟另一个节点的 beacon 发送器
    sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    sock.setsockopt(socket.SOL_SOCKET, socket.SO_BROADCAST, 1)
    card = json.dumps({"v": 1, "node_id": "n-peer-x", "name": "对端X",
                       "address": "http://127.0.0.1:9001"}).encode()

    beacon = UdpBeacon()
    try:
        await beacon.start()
        if beacon._transport is None:
            pytest.skip("环境不支持 UDP 广播绑定")

        # 注意：beacon.start() 也会广播自己（self_id），应被自身过滤。
        # 这里改一下 state 让 beacon 变成"对端X"的广播者
        state.self_id = "n-peer-x"
        state.self_name = "对端X"
        state.address = "http://127.0.0.1:9001"

        # 另起一个"本机"接收端点验证过滤逻辑由 _Protocol 完成：
        # 直接把对端名片发到接收端口
        from app.config import get_settings
        port = get_settings().discovery_port

        # 先恢复 self_id，让接收端把 n-peer-x 当对端
        state.self_id = "n-self-test"
        deadline = asyncio.get_running_loop().time() + 8
        while asyncio.get_running_loop().time() < deadline:
            sock.sendto(card, ("255.255.255.255", port))
            if "n-peer-x" in state.pending:
                break
            await asyncio.sleep(1)
        assert state.pending.get("n-peer-x", {}).get("address") == "http://127.0.0.1:9001"

        # 忽略后不再出现
        state.pending.pop("n-peer-x")
        state.dismissed.add("n-peer-x")
        deadline = asyncio.get_running_loop().time() + 5
        while asyncio.get_running_loop().time() < deadline:
            sock.sendto(card, ("255.255.255.255", port))
            await asyncio.sleep(1)
        assert "n-peer-x" not in state.pending
    finally:
        await beacon.stop()
        sock.close()
        state.enabled = False
        state.self_id = "local"
        state.pending.clear()
        state.dismissed.clear()
