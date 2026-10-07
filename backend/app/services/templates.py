"""内置模板加载：扫描 templates_builtin/*.yaml，按 key 幂等 upsert 入库。"""
import logging
from pathlib import Path

import yaml
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from ..models import Template

log = logging.getLogger("linkhub.templates")


async def sync_builtin_templates(db: AsyncSession, templates_dir: Path) -> int:
    if not templates_dir.is_dir():
        log.warning("内置模板目录不存在: %s", templates_dir)
        return 0
    count = 0
    for path in sorted(templates_dir.glob("*.yaml")):
        data = yaml.safe_load(path.read_text(encoding="utf-8"))
        if not data or "key" not in data or "name" not in data:
            log.warning("模板文件缺少 key/name，跳过: %s", path)
            continue
        row = (await db.execute(select(Template).where(Template.key == data["key"]))).scalar_one_or_none()
        fields = dict(
            name=data["name"],
            category=data.get("category", "开发板"),
            icon=data.get("icon", "🧩"),
            description=data.get("description", ""),
            spec=data.get("spec", {}),
            default_connections=data.get("default_connections", []),
            builtin=True,
        )
        if row is None:
            db.add(Template(key=data["key"], **fields))
        elif row.builtin:  # 只更新仍为内置的，不覆盖用户改过的自定义模板
            for k, v in fields.items():
                setattr(row, k, v)
        count += 1
    await db.commit()
    log.info("内置模板同步完成，共 %d 个", count)
    return count
