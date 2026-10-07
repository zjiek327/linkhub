"""测试公共夹具：临时 SQLite 库 + TestClient（自动跑 lifespan，含模板同步）。"""
import os
import tempfile

_TMP = tempfile.mkdtemp(prefix="linkhub-test-")
os.environ["LINKHUB_DATABASE_URL"] = f"sqlite+aiosqlite:///{_TMP}/test.db"
os.environ.setdefault("LINKHUB_SECRET_KEY", "test-secret")

import pytest
from starlette.testclient import TestClient

from app.main import create_app


@pytest.fixture(scope="session")
def client():
    app = create_app()
    with TestClient(app) as c:  # 进入 with 才执行 lifespan
        yield c


@pytest.fixture()
def device(client):
    resp = client.post("/api/devices", json={"name": "测试树莓派", "tags": ["lab"]})
    assert resp.status_code == 201
    return resp.json()
