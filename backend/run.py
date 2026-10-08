"""PyInstaller 入口：用绝对导入启动应用（相对导入在冻结后不可用）。"""
import os

import uvicorn
from app.main import app  # noqa: F401

if __name__ == "__main__":
    port = int(os.environ.get("LINKHUB_PORT", "8000"))
    uvicorn.run(app, host="0.0.0.0", port=port, log_level="info")
