"""模板 API：内置模板 + 用户自定义模板。"""
from fastapi import APIRouter, Depends, HTTPException
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from ..auth import require_admin, require_operator
from ..database import get_db
from ..models import Template
from ..schemas import TemplateIn, TemplateOut

router = APIRouter(prefix="/api/templates", tags=["templates"])


@router.get("", response_model=list[TemplateOut])
async def list_templates(db: AsyncSession = Depends(get_db)):
    return (await db.execute(select(Template).order_by(Template.builtin.desc(), Template.id))
            ).scalars().all()


@router.get("/{key}", response_model=TemplateOut)
async def get_template(key: str, db: AsyncSession = Depends(get_db)):
    row = (await db.execute(select(Template).where(Template.key == key))).scalar_one_or_none()
    if row is None:
        raise HTTPException(404, f"模板 {key} 不存在")
    return row


@router.post("", response_model=TemplateOut, status_code=201, dependencies=[Depends(require_operator)])
async def create_template(body: TemplateIn, db: AsyncSession = Depends(get_db)):
    exists = (await db.execute(select(Template).where(Template.key == body.key))).scalar_one_or_none()
    if exists:
        raise HTTPException(409, f"模板 key {body.key} 已存在")
    row = Template(**body.model_dump(), builtin=False)
    db.add(row)
    await db.commit()
    await db.refresh(row)
    return row


@router.delete("/{key}", status_code=204, dependencies=[Depends(require_admin)])
async def delete_template(key: str, db: AsyncSession = Depends(get_db)):
    row = (await db.execute(select(Template).where(Template.key == key))).scalar_one_or_none()
    if row is None:
        raise HTTPException(404, f"模板 {key} 不存在")
    if row.builtin:
        raise HTTPException(400, "内置模板不可删除")
    await db.delete(row)
    await db.commit()
