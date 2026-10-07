"""API 路由聚合。"""
from fastapi import APIRouter

from . import connections, devices, serial, sessions, templates, ws

api_router = APIRouter()
for r in (devices.router, templates.router, connections.router, serial.router, sessions.router):
    api_router.include_router(r)

ws_router = ws.router
