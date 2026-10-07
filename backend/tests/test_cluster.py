"""集群 E2E：起两个真实后端进程（节点 A/B），验证 join、联邦列表、终端中继、批量执行、离线标灰。

pty 伪终端模拟串口设备；回显线程模拟设备应答。
"""
import os
import pty
import subprocess
import sys
import tempfile
import threading
import time

import httpx
import pytest
from websockets.sync.client import connect as ws_connect

BACKEND = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
PORT_A, PORT_B = 18101, 18102
A, B = f"http://127.0.0.1:{PORT_A}", f"http://127.0.0.1:{PORT_B}"


def _start_node(port: int, name: str, db_path: str) -> subprocess.Popen:
    env = {**os.environ,
           "LINKHUB_DATABASE_URL": f"sqlite+aiosqlite:///{db_path}",
           "LINKHUB_CLUSTER_ENABLED": "true",
           "LINKHUB_CLUSTER_TOKEN": "test-token",
           "LINKHUB_NODE_NAME": name,
           "LINKHUB_ADVERTISE_ADDR": f"http://127.0.0.1:{port}",
           "LINKHUB_HEARTBEAT_INTERVAL": "0.5",
           "LINKHUB_HEARTBEAT_TIMEOUT": "3",
           "LINKHUB_DIRECTORY_SYNC_INTERVAL": "1",
           "LINKHUB_CLUSTER_MDNS": "false"}
    return subprocess.Popen(
        [sys.executable, "-m", "uvicorn", "app.main:app", "--port", str(port)],
        cwd=BACKEND, env=env,
        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


def _wait_health(url: str, timeout=20):
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            if httpx.get(f"{url}/api/health", timeout=1).status_code == 200:
                return
        except httpx.HTTPError:
            time.sleep(0.2)
    raise RuntimeError(f"{url} 启动超时")


class EchoPty:
    """pty 回显设备：读到什么回什么，模拟串口对端。"""

    def __init__(self):
        self.master, slave = pty.openpty()
        self.slave_name = os.ttyname(slave)
        self._stop = threading.Event()
        self._t = threading.Thread(target=self._echo, daemon=True)
        self._t.start()

    def _echo(self):
        while not self._stop.is_set():
            try:
                data = os.read(self.master, 1024)
                if data:
                    os.write(self.master, b"ok:" + data)
            except OSError:
                return

    def close(self):
        self._stop.set()
        os.close(self.master)


@pytest.fixture(scope="module")
def cluster(tmp_path_factory):
    tmp = tmp_path_factory.mktemp("cluster")
    pa = _start_node(PORT_A, "节点A", str(tmp / "a.db"))
    pb = _start_node(PORT_B, "节点B", str(tmp / "b.db"))
    _wait_health(A)
    _wait_health(B)
    yield {"A": A, "B": B, "proc_b": pb}
    for p in (pa, pb):
        p.terminate()
        p.wait(timeout=5)


def _node_id(url: str) -> str:
    return httpx.get(f"{url}/api/cluster/info").json()["node_id"]


def _wait_until(fn, timeout=8, interval=0.3):
    deadline = time.time() + timeout
    while time.time() < deadline:
        v = fn()
        if v:
            return v
        time.sleep(interval)
    raise AssertionError("等待超时")


def test_internal_requires_token(cluster):
    assert httpx.get(f"{A}/api/cluster/internal/ping").status_code == 403
    r = httpx.get(f"{A}/api/cluster/internal/ping", headers={"X-LinkHub-Token": "test-token"})
    assert r.status_code == 200 and r.json()["name"] == "节点A"


def test_join_and_federated_list(cluster):
    a_id, b_id = _node_id(A), _node_id(B)
    # B 加入 A
    r = httpx.post(f"{B}/api/cluster/join", json={"address": A})
    assert r.status_code == 201, r.text
    assert r.json()["node_id"] == a_id

    # 双方都应看到对方 online
    def both_online():
        a_sees = {n["node_id"]: n for n in httpx.get(f"{A}/api/cluster/nodes").json()}
        b_sees = {n["node_id"]: n for n in httpx.get(f"{B}/api/cluster/nodes").json()}
        return (a_sees.get(b_id, {}).get("status") == "online"
                and b_sees.get(a_id, {}).get("status") == "online")
    _wait_until(both_online)

    # 在 B 上创建设备 → 应出现在 A 的联邦列表
    r = httpx.post(f"{B}/api/devices", json={"name": "B机-树莓派", "tags": ["remote"]})
    assert r.status_code == 201
    dev_b = r.json()

    def found():
        items = httpx.get(f"{A}/api/devices").json()["items"]
        return next((d for d in items if d["node_id"] == b_id and d["id"] == dev_b["id"]), None)
    remote = _wait_until(found)
    assert remote["name"] == "B机-树莓派"
    assert remote["node_name"] == "节点B"
    assert remote["node_online"] is True
    cluster["dev_b"] = dev_b


def test_remote_terminal_relay(cluster):
    """从节点 A 打开节点 B 上设备的终端：A 做中继，pty 回显。"""
    b_id = _node_id(B)
    echo = EchoPty()
    try:
        dev = httpx.post(f"{B}/api/devices", json={"name": "B机-中继测试"}).json()
        conn = httpx.post(f"{B}/api/devices/{dev['id']}/connections", json={
            "kind": "serial", "name": "UART",
            "params": {"port": echo.slave_name, "baudrate": 9600, "auto_reconnect": False},
        }).json()

        # 经 A 代理打开 B 的会话
        r = httpx.post(f"{A}/api/cluster/proxy/{b_id}/open", json={"connection_id": conn["id"]})
        assert r.status_code == 201, r.text
        sid = r.json()["session_id"]
        assert r.json()["node_id"] == b_id

        # 经 A 的中继 WS 收发
        with ws_connect(f"ws://127.0.0.1:{PORT_A}/ws/terminal/{sid}?node={b_id}") as ws:
            ws.send(b"whoami\r")
            data = ws.recv(timeout=5)
            assert b"ok:whoami" in data  # B 侧 pty 回显经中继回到浏览器
        httpx.post(f"{A}/api/cluster/proxy/{b_id}/sessions/{sid}/close")
    finally:
        echo.close()


def test_batch_exec_cross_node(cluster):
    """跨节点批量执行：A 一条命令同时下发本机与 B 的设备。"""
    b_id = _node_id(B)
    echo_a, echo_b = EchoPty(), EchoPty()
    try:
        dev_a = httpx.post(f"{A}/api/devices", json={"name": "A机-批量"}).json()
        dev_b = httpx.post(f"{B}/api/devices", json={"name": "B机-批量"}).json()
        for url, dev, echo in ((A, dev_a, echo_a), (B, dev_b, echo_b)):
            httpx.post(f"{url}/api/devices/{dev['id']}/connections", json={
                "kind": "serial", "name": "UART",
                "params": {"port": echo.slave_name, "baudrate": 9600, "auto_reconnect": False},
            })

        r = httpx.post(f"{A}/api/cluster/batch/exec", json={
            "targets": [{"node_id": "local", "device_id": dev_a["id"]},
                        {"node_id": b_id, "device_id": dev_b["id"]}],
            "command": "uname -a", "wait_ms": 1000,
        }, timeout=30)
        assert r.status_code == 200, r.text
        results = {x["device_id"]: x for x in r.json()["results"]}
        assert results[dev_a["id"]]["ok"] and "ok:uname" in results[dev_a["id"]]["output"]
        assert results[dev_b["id"]]["ok"] and "ok:uname" in results[dev_b["id"]]["output"]
        assert results[dev_b["id"]]["node_id"] == b_id
    finally:
        echo_a.close()
        echo_b.close()


def test_node_offline_grays_devices(cluster):
    """最后执行：杀掉 B → A 应把 B 标离线；重启 B → 自动恢复在线（重启恢复回归）。"""
    b_id = _node_id(B)
    cluster["proc_b"].terminate()
    cluster["proc_b"].wait(timeout=5)

    def offline():
        nodes = {n["node_id"]: n for n in httpx.get(f"{A}/api/cluster/nodes").json()}
        return nodes.get(b_id, {}).get("status") == "offline"
    _wait_until(offline, timeout=10)

    items = httpx.get(f"{A}/api/devices").json()["items"]
    remote = next(d for d in items if d["node_id"] == b_id)
    assert remote["node_online"] is False

    # 离线节点代理请求返回 503
    r = httpx.post(f"{A}/api/cluster/proxy/{b_id}/open", json={"connection_id": 1})
    assert r.status_code == 503

    # 重启 B：心跳恢复后应自动回到 online，设备目录重新同步
    tmp = tempfile.mkdtemp(prefix="linkhub-restart-")
    pb = _start_node(PORT_B, "节点B", str(tmp_path_or_db(tmp)))
    _wait_health(B)

    def back_online():
        nodes = {n["node_id"]: n for n in httpx.get(f"{A}/api/cluster/nodes").json()}
        return nodes.get(b_id, {}).get("status") == "online"
    _wait_until(back_online, timeout=15)
    pb.terminate()
    pb.wait(timeout=5)


def tmp_path_or_db(tmp: str) -> str:
    return os.path.join(tmp, "b.db")
