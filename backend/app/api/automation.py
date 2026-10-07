"""P4：脚本编排（playbook 一次性执行）+ 定时任务 CRUD。"""
from fastapi import APIRouter, Depends, HTTPException
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from ..auth import audit, require_operator
from ..database import get_db
from ..models import ScheduledTask, User
from ..auth import current_user
from ..playbook import PlaybookIn, run_playbook
from ..scheduler import track_task, untrack_task

router = APIRouter(prefix="/api", tags=["automation"])


class TaskOut:
    pass


def _task_out(t: ScheduledTask) -> dict:
    return {"id": t.id, "name": t.name, "command": t.command, "targets": t.targets or [],
            "interval_s": t.interval_s, "enabled": t.enabled, "wait_ms": t.wait_ms,
            "last_run": t.last_run.isoformat() if t.last_run else None,
            "history": (t.history or [])[-5:]}


# ---------- Playbook 一次性执行 ----------
@router.post("/playbook/run", dependencies=[Depends(require_operator)])
async def playbook_run(body: PlaybookIn, cur: User = Depends(current_user),
                       db: AsyncSession = Depends(get_db)):
    await audit(db, cur.name, "playbook_run", body.name, f"{len(body.steps)} 步")
    return await run_playbook(body)


# ---------- 定时任务 ----------
@router.get("/tasks", dependencies=[Depends(require_operator)])
async def list_tasks(db: AsyncSession = Depends(get_db)):
    rows = (await db.execute(select(ScheduledTask).order_by(ScheduledTask.id))).scalars().all()
    return [_task_out(r) for r in rows]


@router.post("/tasks", status_code=201, dependencies=[Depends(require_operator)])
async def create_task(body: dict, db: AsyncSession = Depends(get_db),
                      cur: User = Depends(current_user)):
    if not body.get("name") or not body.get("command"):
        raise HTTPException(400, "name 与 command 必填")
    row = ScheduledTask(name=body["name"], command=body["command"],
                        targets=body.get("targets", []),
                        interval_s=max(30, int(body.get("interval_s", 300))),
                        wait_ms=body.get("wait_ms", 1500),
                        enabled=body.get("enabled", True))
    db.add(row)
    await db.commit()
    await db.refresh(row)
    if row.enabled:
        track_task(row.id)
    await audit(db, cur.name, "task_create", row.name)
    return _task_out(row)


@router.put("/tasks/{task_id}", dependencies=[Depends(require_operator)])
async def update_task(task_id: int, body: dict, db: AsyncSession = Depends(get_db),
                      cur: User = Depends(current_user)):
    row = await db.get(ScheduledTask, task_id)
    if row is None:
        raise HTTPException(404, "任务不存在")
    for k in ("name", "command", "targets", "interval_s", "enabled", "wait_ms"):
        if k in body:
            setattr(row, k, body[k])
    await db.commit()
    untrack_task(task_id)
    if row.enabled:
        track_task(task_id)
    await audit(db, cur.name, "task_update", row.name)
    return _task_out(row)


@router.delete("/tasks/{task_id}", status_code=204, dependencies=[Depends(require_operator)])
async def delete_task(task_id: int, db: AsyncSession = Depends(get_db),
                      cur: User = Depends(current_user)):
    row = await db.get(ScheduledTask, task_id)
    if row is None:
        raise HTTPException(404, "任务不存在")
    untrack_task(task_id)
    await audit(db, cur.name, "task_delete", row.name)
    await db.delete(row)
    await db.commit()
