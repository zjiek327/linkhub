"""认证/RBAC/凭证/编排/定时任务 测试。"""


def test_login_and_rbac(client):
    # 错误密码
    r = client.post("/api/auth/login", json={"name": "admin", "password": "wrong"})
    assert r.status_code == 401

    # viewer 用户被写接口拒绝
    client.post("/api/users", json={"name": "v1", "password": "p", "role": "viewer"})
    r = client.post("/api/auth/login", json={"name": "v1", "password": "p"})
    assert r.status_code == 200
    viewer_headers = {"Authorization": f"Bearer {r.json()['token']}"}

    r = client.post("/api/devices", json={"name": "viewer设备"},
                    headers=viewer_headers)
    assert r.status_code == 403           # viewer 不能建设备
    r = client.get("/api/devices", headers=viewer_headers)
    assert r.status_code == 200           # 能看列表
    assert client.get("/api/users", headers=viewer_headers).status_code == 403
    assert client.get("/api/users").status_code == 200            # admin 能管用户
    assert client.get("/api/audit").status_code == 200            # admin 能看审计

    # operator 可以建设备
    client.post("/api/users", json={"name": "op1", "password": "p", "role": "operator"})
    r = client.post("/api/auth/login", json={"name": "op1", "password": "p"})
    op_headers = {"Authorization": f"Bearer {r.json()['token']}"}
    r = client.post("/api/devices", json={"name": "op设备"}, headers=op_headers)
    assert r.status_code == 201
    client.delete(f"/api/devices/{r.json()['id']}", headers=op_headers)

    # 未登录（无 header 无 cookie）一律 401 —— 用独立 TestClient 实例验证
    from starlette.testclient import TestClient as _TC
    no_auth = _TC(client.app)
    assert no_auth.get("/api/devices").status_code == 401


def test_credential_crud(client):
    r = client.post("/api/credentials", json={"name": "pi密码", "type": "password",
                                              "secret": "raspberry"})
    assert r.status_code == 201
    cred = r.json()
    assert "secret" not in cred                     # 列表不回显密文
    assert client.get("/api/credentials").json()[0]["name"] == "pi密码"
    assert client.delete(f"/api/credentials/{cred['id']}").status_code == 204


def test_playbook_run(client):
    r = client.post("/api/playbook/run", json={
        "name": "巡检", "steps": [
            {"targets": [{"node_id": "local", "device_id": 99999}],
             "command": "echo hi", "wait_ms": 300},
        ]})
    assert r.status_code == 200
    body = r.json()
    assert body["ok"] is False                      # 设备不存在 → 步骤失败
    assert body["steps"][0]["results"][0]["ok"] is False


def test_scheduled_task_crud(client):
    r = client.post("/api/tasks", json={"name": "每5分钟心跳", "command": "echo ok",
                                        "targets": [], "interval_s": 300})
    assert r.status_code == 201
    tid = r.json()["id"]
    tasks = client.get("/api/tasks").json()
    assert any(t["id"] == tid for t in tasks)
    r = client.put(f"/api/tasks/{tid}", json={"enabled": False})
    assert r.status_code == 200 and r.json()["enabled"] is False
    assert client.delete(f"/api/tasks/{tid}").status_code == 204


def test_connector_kinds_include_new(client):
    kinds = client.get("/api/connector-kinds").json()
    ks = {k["kind"] for k in kinds}
    assert {"serial", "ssh", "telnet", "mqtt", "ble"} <= ks
    ssh_schema = next(k for k in kinds if k["kind"] == "ssh")["schema"]
    assert "host" in ssh_schema["properties"]
