"""模板接口测试。"""

BUILTIN_KEYS = {"raspberry_pi_4b", "raspberry_pi_5", "banana_pi_m4",
                "orange_pi_5", "esp32_devkit", "arduino_uno"}


def test_builtin_templates_loaded(client):
    tpls = client.get("/api/templates").json()
    keys = {t["key"] for t in tpls}
    assert BUILTIN_KEYS <= keys
    rpi = next(t for t in tpls if t["key"] == "raspberry_pi_4b")
    assert rpi["builtin"] is True
    assert rpi["default_connections"][0]["params"]["baudrate"] == 115200


def test_orange_pi_special_baudrate(client):
    tpl = client.get("/api/templates/orange_pi_5").json()
    assert tpl["default_connections"][0]["params"]["baudrate"] == 1500000


def test_custom_template_crud(client):
    r = client.post("/api/templates", json={
        "key": "my_board", "name": "我的板子",
        "default_connections": [{"kind": "serial", "name": "UART", "params": {"port": "/dev/ttyUSB0"}}],
    })
    assert r.status_code == 201
    assert client.post("/api/templates", json={"key": "my_board", "name": "x"}).status_code == 409
    assert client.delete("/api/templates/my_board").status_code == 204


def test_builtin_template_delete_forbidden(client):
    assert client.delete("/api/templates/raspberry_pi_4b").status_code == 400


def test_connector_kinds_schema(client):
    kinds = client.get("/api/connector-kinds").json()
    serial = next(k for k in kinds if k["kind"] == "serial")
    assert "port" in serial["schema"]["properties"]
