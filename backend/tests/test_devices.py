"""设备 / 分组 / 统计接口测试。"""


def test_group_crud(client):
    r = client.post("/api/groups", json={"name": "实验室A"})
    assert r.status_code == 201
    gid = r.json()["id"]
    assert any(g["id"] == gid for g in client.get("/api/groups").json())
    assert client.delete(f"/api/groups/{gid}").status_code == 204


def test_device_crud(client):
    r = client.post("/api/devices", json={
        "name": "机柜-01", "description": "树莓派4B", "tags": ["4G", "机柜"], "location": "A-01",
    })
    assert r.status_code == 201
    dev = r.json()
    assert dev["online"] is False

    r = client.get(f"/api/devices/{dev['id']}")
    assert r.status_code == 200 and r.json()["name"] == "机柜-01"

    r = client.put(f"/api/devices/{dev['id']}", json={"name": "机柜-01改", "tags": []})
    assert r.status_code == 200 and r.json()["name"] == "机柜-01改"

    listed = client.get("/api/devices", params={"keyword": "机柜-01改"}).json()
    assert listed["total"] >= 1

    assert client.delete(f"/api/devices/{dev['id']}").status_code == 204
    assert client.get(f"/api/devices/{dev['id']}").status_code == 404


def test_create_from_template(client):
    r = client.post("/api/devices/from-template/raspberry_pi_4b",
                    json={"name": "我的派", "param_overrides": {"0": {"port": "/dev/ttyUSB9"}}})
    assert r.status_code == 201
    dev = r.json()
    assert dev["template_id"] is not None

    conns = client.get(f"/api/devices/{dev['id']}/connections").json()
    assert len(conns) == 1
    assert conns[0]["kind"] == "serial"
    assert conns[0]["params"]["baudrate"] == 115200
    assert conns[0]["params"]["port"] == "/dev/ttyUSB9"  # 覆盖生效
    client.delete(f"/api/devices/{dev['id']}")


def test_template_not_found(client):
    assert client.post("/api/devices/from-template/no_such", json={"name": "x"}).status_code == 404


def test_stats(client):
    r = client.get("/api/stats")
    assert r.status_code == 200
    body = r.json()
    assert body["templates_total"] >= 6
