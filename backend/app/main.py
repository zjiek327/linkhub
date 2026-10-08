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
        from .auth import ensure_default_admin
        await ensure_default_admin(db)
        # 会话存活于进程内存，重启后把历史未关闭会话标记关闭（防僵尸"在线"）
        from datetime import datetime, timezone
        from sqlalchemy import update
        from .models import Session
        await db.execute(update(Session).where(Session.closed_at.is_(None)).values(
            closed_at=datetime.now(timezone.utc), status="closed",
            last_error="服务重启，会话终止"))
        await db.commit()
    # 集群模式：注册本机身份、拉起后台同步与节点发现（mDNS + UDP 广播）
    await state.start()
    if state.enabled:
        start_background_tasks()
        await mdns.start()
        await beacon.start()
    from .scheduler import start_scheduler, stop_scheduler
    await start_scheduler()
    yield
    await stop_scheduler()
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

    # 生产/安装包模式：后端直接托管前端构建产物（单端口，免单独前端进程）
    from fastapi.responses import FileResponse
    from fastapi.staticfiles import StaticFiles
    from .platform_utils import frontend_dist_dir

    dist = frontend_dist_dir()
    if dist and dist.is_dir():
        app.mount("/assets", StaticFiles(directory=dist / "assets"), name="assets")

        @app.get("/{full_path:path}", include_in_schema=False)
        async def spa_fallback(full_path: str):
            # 已注册路由优先（FastAPI 先匹配）；其余走 SPA
            if full_path.startswith(("api/", "ws/")):
                return None  # 不会走到这：路由已注册过
            index = dist / "index.html"
            return FileResponse(index)

    @app.get("/api/health")
    async def health():
        return {"status": "ok", "app": settings.app_name, "mascot": "🐙连连"}

    return app


app = create_app()
