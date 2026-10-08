"""终端多人协作：主控可写、旁观只读、申请/授权/释放。"""
import json
import os
import pty
import select
import threading
import time

import pytest

pytestmark = pytest.mark.skipif(os.name != 'posix', reason='需要 POSIX pty')



@pytest.fixture()
def pty_pair():
    """pty 伪终端 + 心跳字节流（让 ws.receive 持续返回，_recv_ctrl 的超时才有效）。"""
    master, slave = pty.openpty()
    stop = threading.Event()
    def heartbeat():
        while not stop.is_set():
            try:
                os.write(master, b".")
            except OSError:
                return
            time.sleep(0.2)
    t = threading.Thread(target=heartbeat, daemon=True)
    t.start()
    yield master, os.ttyname(slave)
    stop.set()
    os.close(master)
    os.close(slave)


def _recv_ctrl(ws, timeout=3, skip=("presence",)):
    """收一条控制消息（跳过二进制终端数据帧与 presence 广播）。"""
    deadline = time.time() + timeout
    while time.time() < deadline:
        msg = ws.receive()
        if msg.get("text"):
            body = json.loads(msg["text"])
            if "lh" in body and body["lh"].get("type") not in skip:
                return body["lh"]
    raise TimeoutError("没等到控制消息")


def _master_got(master, needle: bytes, timeout=2) -> bool:
    deadline = time.time() + timeout
    buf = b""
    while time.time() < deadline:
        r, _, _ = select.select([master], [], [], 0.2)
        if r:
            buf += os.read(master, 1024)
            if needle in buf:
                return True
    return False


def test_writer_viewer_flow(client, pty_pair):
    master, slave_name = pty_pair
    dev = client.post("/api/devices", json={"name": "协作测试"}).json()
    conn = client.post(f"/api/devices/{dev['id']}/connections", json={
        "kind": "serial", "name": "UART",
        "params": {"port": slave_name, "baudrate": 9600, "auto_reconnect": False},
    }).json()
    sid = client.post(f"/api/connections/{conn['id']}/open").json()["id"]
    time.sleep(1)

    with client.websocket_connect(f"/ws/terminal/{sid}?cid=c1&name=主控") as ws1:
        role1 = _recv_ctrl(ws1)
        assert role1["type"] == "role" and role1["writer"] == "c1"  # 先进为主控

        with client.websocket_connect(f"/ws/terminal/{sid}?cid=c2&name=旁观B") as ws2:
            role2 = _recv_ctrl(ws2)
            assert role2["type"] == "role" and role2["writer"] == "c1"  # 旁观

            # 旁观输入被丢弃
            ws2.send_text("pwd\r")
            assert _recv_ctrl(ws2)["type"] == "input_blocked"
            assert not _master_got(master, b"pwd", timeout=0.8)

            # 旁观申请输入 → 主控收到请求
            ws2.send_text(json.dumps({"lh": {"type": "request_write", "name": "旁观B"}}))
            req = _recv_ctrl(ws1)
            assert req["type"] == "input_request" and req["from"] == "c2"

            # 主控同意 → 双方收到角色变更
            ws1.send_text(json.dumps({"lh": {"type": "grant_write", "to": "c2"}}))
            change = _recv_ctrl(ws2)
            assert change["type"] == "role_change" and change["writer"] == "c2"
            assert _recv_ctrl(ws1)["type"] == "role_change"  # 主控也收到广播

            # 现在旁观（已转正）能写入
            ws2.send_text("whoami\r")
            assert _master_got(master, b"whoami")
            # 原主控被降级，输入被丢弃
            ws1.send_text("id\r")
            assert _recv_ctrl(ws1)["type"] == "input_blocked"
            assert not _master_got(master, b"id\r", timeout=0.8)

            # 主控（现c2）释放 → writer_free 广播
            ws2.send_text(json.dumps({"lh": {"type": "release_write"}}))
            free = _recv_ctrl(ws1)
            assert free["type"] == "writer_free"

    client.post(f"/api/sessions/{sid}/close")
    client.delete(f"/api/devices/{dev['id']}")


def test_single_client_default_writer(client, pty_pair):
    """无 cid 的裸连（兼容旧行为）：自动分配 cid 且默认主控。"""
    master, slave_name = pty_pair
    dev = client.post("/api/devices", json={"name": "裸连测试"}).json()
    conn = client.post(f"/api/devices/{dev['id']}/connections", json={
        "kind": "serial", "name": "UART",
        "params": {"port": slave_name, "baudrate": 9600, "auto_reconnect": False},
    }).json()
    sid = client.post(f"/api/connections/{conn['id']}/open").json()["id"]
    time.sleep(1)
    with client.websocket_connect(f"/ws/terminal/{sid}") as ws:
        role = _recv_ctrl(ws)
        assert role["type"] == "role" and role["writer"] == role["me"]
        ws.send_text("ls\r")
        assert _master_got(master, b"ls")
    client.post(f"/api/sessions/{sid}/close")
    client.delete(f"/api/devices/{dev['id']}")
