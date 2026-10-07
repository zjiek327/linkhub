"""灵枢 LinkHub 后端入口。"""
import logging
from contextlib import asynccontextmanager

from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware

from .api import api_router, ws_router
from .cluster.beacon import beacon
from .cluster.mdns import mdns
from .cluster.state import state
from .cluster.sync import start_background_tasks
from .config import get_settings
from .database import SessionLocal, init_db
from .services.templates import sync_builtin_templates
from .session_manager import manager

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(name)s %(levelname)s %(message)s")


@asynccontextmanager
async def lifespan(app: FastAPI):
    settings = get_settings()
    await init_db()
    async with SessionLocal() as db:
        await sync_builtin_templates(db, settings.builtin_templates_dir)
    # 集群模式：注册本机身份、拉起后台同步与节点发现（mDNS + UDP 广播）
    await state.start()
    if state.enabled:
        start_background_tasks()
        await mdns.start()
        await beacon.start()
    yield
    await beacon.stop()
    await mdns.stop()
    await state.stop()
    await manager.close_all()


def create_app() -> FastAPI:
    settings = get_settings()
    app = FastAPI(title=settings.app_name, version="0.1.0", lifespan=lifespan)
    app.add_middleware(
        CORSMiddleware,
        allow_origins=settings.cors_origins,
        allow_methods=["*"],
        allow_headers=["*"],
    )
    app.include_router(api_router)
    app.include_router(ws_router)

    @app.get("/api/health")
    async def health():
        return {"status": "ok", "app": settings.app_name, "mascot": "🐙连连"}

    return app


app = create_app()
