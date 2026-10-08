"""异步数据库引擎与会话。"""
from collections.abc import AsyncIterator

from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker, create_async_engine

from .config import get_settings

settings = get_settings()
engine = create_async_engine(settings.db_url, echo=False)
SessionLocal = async_sessionmaker(engine, expire_on_commit=False, class_=AsyncSession)


async def get_db() -> AsyncIterator[AsyncSession]:
    async with SessionLocal() as session:
        yield session


async def init_db() -> None:
    from .models import Base  # 延迟导入避免循环

    async with engine.begin() as conn:
        await conn.run_sync(Base.metadata.create_all)
        # 朴素列迁移：老库补新列（SQLite 不支持 IF NOT EXISTS 加列，用 PRAGMA 判断）
        for table, column, ddl in [
            ("devices", "node_id", "node_id VARCHAR(32) DEFAULT 'local'"),
            ("connection_profiles", "node_id", "node_id VARCHAR(32) DEFAULT 'local'"),
        ]:
            rows = await conn.exec_driver_sql(f"PRAGMA table_info({table})")
            if column not in [r[1] for r in rows.fetchall()]:
                await conn.exec_driver_sql(f"ALTER TABLE {table} ADD COLUMN {ddl}")
