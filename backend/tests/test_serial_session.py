"""串口会话端到端测试：用 pty 伪终端模拟真实串口设备，无需硬件。"""
import base64
import os
import pty
import time

import pytest

pytestmark = pytest.mark.skipif(os.name != 'posix', reason='需要 POSIX pty')



@pytest.fixture()
def pty_pair():
    master, slave = pty.openpty()
    yield master, os.ttyname(slave)
    os.close(master)
    os.close(slave)


def _wait_online(client, session_id, timeout=5):
    deadline = time.time() + timeout
    while time.time() < deadline:
        s = client.get(f"/api/sessions/{session_id}").json()
        if s["status"] == "online":
            return s
        if s["status"] == "error":
            raise AssertionError(f"会话进入 error: {s['last_error']}")
        time.sleep(0.1)
    raise AssertionError("会话上线超时")


def _recv_bytes(ws, timeout=3):
    """收终端数据帧（跳过角色等文本控制帧）。"""
    deadline = time.time() + timeout
    while time.time() < deadline:
        m = ws.receive()
        if m.get("bytes") is not None:
            return m["bytes"]
    raise TimeoutError("没等到数据帧")


def test_serial_session_full_flow(client, pty_pair):
    """打开会话 → 双向收发 → 日志落盘 → 密码遮蔽 → 关闭。全链路模拟树莓派串口登录。"""
    master, slave_name = pty_pair

    dev = client.post("/api/devices", json={"name": "pty-树莓派"}).json()
    conn = client.post(f"/api/devices/{dev['id']}/connections", json={
        "kind": "serial", "name": "UART",
        "params": {"port": slave_name, "baudrate": 9600, "auto_reconnect": False},
    }).json()
    assert conn["id"]

    # 打开会话
    sess = client.post(f"/api/connections/{conn['id']}/open").json()
    sid = sess["id"]
    _wait_online(client, sid)

    # 设备应显示在线
    assert client.get(f"/api/devices/{dev['id']}").json()["online"] is True

    # WebSocket 终端：rx（设备→浏览器）
    with client.websocket_connect(f"/ws/terminal/{sid}") as ws:
        os.write(master, b"raspberrypi login: ")
        data = _recv_bytes(ws)
        assert b"login:" in data

        # tx（浏览器→设备）：模拟输入密码，验证遮蔽
        os.write(master, b"Password:")   # 设备回显密码提示 → 触发遮蔽
        _recv_bytes(ws)
        ws.send_bytes(b"raspberry\r")
        got = b""
        deadline = time.time() + 3
        while time.time() < deadline and b"raspberry\r" not in got:
            ready = os.read(master, 1024)
            got += ready
        assert b"raspberry\r" in got  # 设备端收到了按键
        time.sleep(0.3)  # 等日志落盘再关终端

    # 日志校验：密码被遮蔽，rx 原样记录
    logs = client.get(f"/api/sessions/{sid}/logs").json()
    tx = [l for l in logs if l["direction"] == "tx"]
    rx = [l for l in logs if l["direction"] == "rx"]
    assert any(base64.b64decode(l["data"]) == b"******" for l in tx), "密码应被遮蔽"
    assert all(b"raspberry" not in base64.b64decode(l["data"]) for l in tx)
    assert any(b"login:" in base64.b64decode(l["data"]) for l in rx)

    # 关闭会话
    r = client.post(f"/api/sessions/{sid}/close")
    assert r.status_code == 200 and r.json()["status"] == "closed"
    assert client.get(f"/api/devices/{dev['id']}").json()["online"] is False
    client.delete(f"/api/devices/{dev['id']}")


def test_open_session_port_conflict(client, pty_pair):
    """同一端口不允许两个会话同时占用。"""
    master, slave_name = pty_pair
    dev = client.post("/api/devices", json={"name": "pty-冲突测试"}).json()
    conn = client.post(f"/api/devices/{dev['id']}/connections", json={
        "kind": "serial", "name": "UART",
        "params": {"port": slave_name, "baudrate": 9600, "auto_reconnect": False},
    }).json()
    s1 = client.post(f"/api/connections/{conn['id']}/open")
    assert s1.status_code == 201
    s2 = client.post(f"/api/connections/{conn['id']}/open")
    assert s2.status_code == 409
    client.post(f"/api/sessions/{s1.json()['id']}/close")
    client.delete(f"/api/devices/{dev['id']}")


def test_open_session_bad_port(client):
    """不存在的串口 → 会话进入 error 并携带原因。"""
    dev = client.post("/api/devices", json={"name": "坏端口"}).json()
    conn = client.post(f"/api/devices/{dev['id']}/connections", json={
        "kind": "serial", "name": "UART",
        "params": {"port": "/dev/ttyNOPE0", "baudrate": 9600, "auto_reconnect": False},
    }).json()
    sid = client.post(f"/api/connections/{conn['id']}/open").json()["id"]
    deadline = time.time() + 5
    while time.time() < deadline:
        s = client.get(f"/api/sessions/{sid}").json()
        if s["status"] == "error":
            assert "ttyNOPE0" in s["last_error"]
            break
        time.sleep(0.1)
    else:
        raise AssertionError("应进入 error 状态")
    client.delete(f"/api/devices/{dev['id']}")


def test_serial_ports_endpoint(client):
    r = client.get("/api/serial/ports")
    assert r.status_code == 200
    assert isinstance(r.json(), list)
