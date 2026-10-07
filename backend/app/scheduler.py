"""定时任务（P4）：周期对多设备执行命令并记录结果（巡检/采集）。

实现为进程内 asyncio 循环 + DB 持久化任务定义，重启自动恢复。
"""
import asyncio
import logging
from datetime import datetime, timezone

from pydantic import BaseModel, Field
from sqlalchemy import select

from .batch_exec import batch_exec_local
from .cluster import client as cluster_client
from .cluster.state import state
from .database import SessionLocal
from .events import bus
from .models import ScheduledTask

log = logging.getLogger("linkhub.scheduler")


class TaskIn(BaseModel):
    name: str = Field(min_length=1)
    command: str = Field(min_length=1)
    targets: list[dict] = Field(default_factory=list)  # [{node_id, device_id}]
    interval_s: int = Field(default=300, ge=30, description="执行间隔（秒）")
    enabled: bool = True
    wait_ms: int = 1500


_tasks: dict[int, asyncio.Task] = {}


async def run_once(task: ScheduledTask) -> None:
    groups: dict[str, list[int]] = {}
    for t in (task.targets or []):
        nid = t.get("node_id", "local")
        groups.setdefault(nid if nid not in ("local", state.self_id) else "local",
                          []).append(t["device_id"])
    results = []
    local_ids = groups.pop("local", [])
    if local_ids:
        results += await batch_exec_local(local_ids, task.command, task.wait_ms, state.self_id)
    for nid, dev_ids in groups.items():
        peer = state.peers.get(nid)
        if peer and peer.status == "online":
            try:
                results += await cluster_client.batch_remote(peer, dev_ids, task.command, task.wait_ms)
                continue
            except Exception as exc:
                results += [{"node_id": nid, "device_id": d, "ok": False, "error": str(exc)} for d in dev_ids]
        else:
            results += [{"node_id": nid, "device_id": d, "ok": False, "error": "节点不在线"} for d in dev_ids]
    # 记录到 history JSON 字段（保留最近 20 条）
    task.history = (task.history or [])[-19:] + [{
        "ts": datetime.now(timezone.utc).isoformat(),
        "ok": all(r.get("ok") for r in results),
        "results": results,
    }]
    task.last_run = datetime.now(timezone.utc)
    bus.publish("task_ran", {"task_id": task.id, "name": task.name})


async def _loop(task_id: int) -> None:
    while True:
        await asyncio.sleep(1)
        async with SessionLocal() as db:
            task = await db.get(ScheduledTask, task_id)
            if task is None or not task.enabled:
                return
            interval = task.interval_s
            since = (datetime.now(timezone.utc) - task.last_run).total_seconds() \
                if task.last_run else interval + 1
            if since < interval:
                continue
            try:
                await run_once(task)
                await db.commit()
            except Exception as exc:
                log.warning("定时任务 %s 执行失败: %s", task.name, exc)


async def start_scheduler() -> None:
    async with SessionLocal() as db:
        tasks = (await db.execute(select(ScheduledTask).where(
            ScheduledTask.enabled.is_(True)))).scalars().all()
    for t in tasks:
        _tasks[t.id] = asyncio.create_task(_loop(t.id))
    log.info("定时任务调度器已启动，%d 个任务", len(_tasks))


def track_task(task_id: int) -> None:
    old = _tasks.pop(task_id, None)
    if old:
        old.cancel()
    _tasks[task_id] = asyncio.create_task(_loop(task_id))


def untrack_task(task_id: int) -> None:
    old = _tasks.pop(task_id, None)
    if old:
        old.cancel()


async def stop_scheduler() -> None:
    for t in _tasks.values():
        t.cancel()
    _tasks.clear()
