# -*- mode: python ; coding: utf-8 -*-
"""灵枢 LinkHub 后端 PyInstaller spec（onedir，启动比 onefile 快）。

用法：先 `npm run build` 前端，再在 backend/ 下 `pyinstaller linkhub-backend.spec`
产物：dist/LinkHubBackend/LinkHubBackend(.exe)
"""
import sys
from pathlib import Path

ROOT = Path.cwd()  # backend/
sys.path.insert(0, str(ROOT))

block_cipher = None

datas = [(str(ROOT / "templates_builtin"), "templates_builtin")]
dist = ROOT.parent / "frontend" / "dist"
if dist.is_dir():
    datas.append((str(dist), "app/dist"))

a = Analysis(
    [str(ROOT / "run.py")],          # 入口：绝对导入，冻结后无相对导入问题
    pathex=[str(ROOT)],
    binaries=[],
    datas=datas,
    hiddenimports=[
        # FastAPI / ASGI
        "uvicorn", "uvicorn.lifespan", "uvicorn.lifespan.on", "uvicorn.logging",
        "uvicorn.protocols", "uvicorn.protocols.http", "uvicorn.protocols.http.h11_impl",
        "uvicorn.protocols.websockets", "uvicorn.protocols.websockets.websockets_impl",
        "uvicorn.loops", "uvicorn.loops.auto", "uvicorn.loops.asyncio",
        # 串口/网络
        "serial", "serial_asyncio", "serial.tools.list_ports",
        "asyncssh", "telnetlib3",
        # 蓝牙/IoT
        "bleak", "aiomqtt",
        # 数据库/安全
        "sqlalchemy", "aiosqlite", "cryptography", "jwt", "pydantic_settings",
        # 发现
        "zeroconf",
        # 应用内（PyInstaller 不会自动跟相对导入链）
        "app.api", "app.api.auth_api", "app.api.automation", "app.api.cluster",
        "app.api.cluster_internal", "app.api.connections", "app.api.devices",
        "app.api.serial", "app.api.sessions", "app.api.templates", "app.api.ws",
        "app.cluster", "app.cluster.beacon", "app.cluster.client",
        "app.cluster.events_link", "app.cluster.mdns", "app.cluster.state",
        "app.cluster.sync",
        "app.connectors", "app.connectors.base", "app.connectors.serial_connector",
        "app.connectors.ssh_connector", "app.connectors.telnet_connector",
        "app.connectors.ble_connector", "app.connectors.mqtt_connector",
        "app.services", "app.services.crypto", "app.services.devices",
        "app.services.templates",
    ],
    hookspath=[],
    runtime_hooks=[],
    excludes=["matplotlib", "numpy", "PIL", "pandas", "tkinter"],
    cipher=block_cipher,
    noarchive=False,
)

pyz = PYZ(a.pure, a.zipped_data, cipher=block_cipher)

exe = EXE(
    pyz, a.scripts, [],
    exclude_binaries=True,
    name="LinkHubBackend",
    debug=False,
    strip=False,
    upx=False,
    console=True,
)

coll = COLLECT(exe, a.binaries, a.zipfiles, a.datas, strip=False, upx=False,
               name="LinkHubBackend")
