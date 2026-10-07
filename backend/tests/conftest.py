"""测试公共夹具：临时 SQLite 库 + TestClient（自动跑 lifespan，含模板同步）。"""
import os
import tempfile

_TMP = tempfile.mkdtemp(prefix="linkhub-test-")
os.environ["LINKHUB_DATABASE_URL"] = f"sqlite+aiosqlite:///{_TMP}/test.db"
os.environ.setdefault("LINKHUB_SECRET_KEY", "test-secret")
# 测试用独立发现端口，避免广播包污染同机上运行中的真实节点
os.environ["LINKHUB_DISCOVERY_PORT"] = "37899"

import pytest
from starlette.testclient import TestClient

from app.main import create_app


@pytest.fixture(scope="session")
def client():
    app = create_app()
    with TestClient(app) as c:  # 进入 with 才执行 lifespan
        # RBAC：登录默认管理员（lifespan 已创建 admin/admin）
        r = c.post("/api/auth/login", json={"name": "admin", "password": "admin"})
        assert r.status_code == 200, r.text
        token = r.json()["token"]
        c.headers.update({"Authorization": f"Bearer {token}"})
        yield c


@pytest.fixture()
def device(client):
    resp = client.post("/api/devices", json={"name": "测试树莓派", "tags": ["lab"]})
    assert resp.status_code == 201
    return resp.json()
