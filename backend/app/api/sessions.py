"""会话查询 / 关闭 / 日志 API。"""
from fastapi import APIRouter, Depends, HTTPException, Query
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from ..auth import require_operator, require_viewer
from ..database import get_db
from ..models import Session, SessionLog
from ..schemas import SessionLogOut, SessionOut
from ..session_manager import manager

router = APIRouter(prefix="/api/sessions", tags=["sessions"])


@router.get("", response_model=list[SessionOut])
async def list_sessions(status: str | None = None, db: AsyncSession = Depends(get_db)):
    stmt = select(Session).order_by(Session.id.desc())
    if status:
        stmt = stmt.where(Session.status == status)
    rows = (await db.execute(stmt)).scalars().all()
    # 运行态覆盖 DB 状态（重连中等瞬态）
    for r in rows:
        rt = manager.get(r.id)
        if rt:
            r.status, r.last_error = rt.status, rt.last_error
    return rows


@router.get("/{session_id}", response_model=SessionOut)
async def get_session(session_id: int, db: AsyncSession = Depends(get_db)):
    row = await db.get(Session, session_id)
    if row is None:
        raise HTTPException(404, "会话不存在")
    rt = manager.get(session_id)
    if rt:
        row.status, row.last_error = rt.status, rt.last_error
    return row


@router.post("/{session_id}/close", response_model=SessionOut, dependencies=[Depends(require_operator)])
async def close_session(session_id: int, db: AsyncSession = Depends(get_db)):
    row = await db.get(Session, session_id)
    if row is None:
        raise HTTPException(404, "会话不存在")
    await manager.close(session_id)
    await db.refresh(row)
    row.status = "closed"
    return row


@router.get("/{session_id}/logs", response_model=list[SessionLogOut])
async def session_logs(
    session_id: int,
    after_id: int = Query(0, ge=0),
    limit: int = Query(500, ge=1, le=5000),
    db: AsyncSession = Depends(get_db),
):
    if await db.get(Session, session_id) is None:
        raise HTTPException(404, "会话不存在")
    stmt = (select(SessionLog)
            .where(SessionLog.session_id == session_id, SessionLog.id > after_id)
            .order_by(SessionLog.id).limit(limit))
    return (await db.execute(stmt)).scalars().all()


@router.get("/{session_id}/logs/export")
async def export_logs(session_id: int, db: AsyncSession = Depends(get_db)):
    """导出会话日志为纯文本（密码行已是 ******）。"""
    import base64
    from fastapi.responses import Response
    rows = (await db.execute(select(SessionLog).where(
        SessionLog.session_id == session_id).order_by(SessionLog.id))).scalars().all()
    lines = []
    for r in rows:
        try:
            data = base64.b64decode(r.data).decode("utf-8", "replace")
        except Exception:
            data = r.data
        prefix = {"rx": "", "tx": "› ", "meta": "# "}.get(r.direction, "? ")
        lines.append(f"[{r.ts.isoformat() if r.ts else ''}] {prefix}{data}")
    return Response("\n".join(lines), media_type="text/plain",
                    headers={"Content-Disposition":
                             f'attachment; filename="linkhub-session-{session_id}.log"'})
