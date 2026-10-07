"""脚本编排（P4）：多设备命令序列，串行/并行执行，步骤间可设等待。

playbook = [{targets: [(node_id, device_id)...], command, wait_ms, parallel: bool}]
"""
import asyncio
from dataclasses import dataclass

from pydantic import BaseModel, Field

from .batch_exec import batch_exec_local
from .cluster import client as cluster_client
from .cluster.state import state


class StepIn(BaseModel):
    targets: list[dict] = Field(default_factory=list)  # [{node_id, device_id}]
    command: str = Field(min_length=1)
    wait_ms: int = Field(default=1500, ge=100, le=60000)
    parallel: bool = True   # 步内多设备并行；False=逐台


class PlaybookIn(BaseModel):
    name: str = ""
    steps: list[StepIn]


@dataclass
class StepResult:
    step: int
    ok: bool
    results: list[dict]

    def dict(self):
        return {"step": self.step, "ok": self.ok, "results": self.results}


async def run_step(step: StepIn, idx: int) -> StepResult:
    results: list[dict] = []
    groups: dict[str, list[int]] = {}
    for t in step.targets:
        nid = t.get("node_id", "local")
        groups.setdefault(nid if nid not in ("local", state.self_id) else "local",
                          []).append(t["device_id"])

    local_ids = groups.pop("local", [])
    if local_ids:
        if step.parallel:
            results += await batch_exec_local(local_ids, step.command, step.wait_ms,
                                              node_id=state.self_id)
        else:
            for did in local_ids:
                results += await batch_exec_local([did], step.command, step.wait_ms,
                                                  node_id=state.self_id)
    for nid, dev_ids in groups.items():
        peer = state.peers.get(nid)
        if peer is None or peer.status != "online":
            results += [{"node_id": nid, "device_id": d, "device_name": f"#{d}",
                         "ok": False, "output": "", "error": "节点不在线"} for d in dev_ids]
            continue
        try:
            if step.parallel:
                results += await cluster_client.batch_remote(peer, dev_ids, step.command, step.wait_ms)
            else:
                for d in dev_ids:
                    results += await cluster_client.batch_remote(peer, [d], step.command, step.wait_ms)
        except Exception as exc:
            results += [{"node_id": nid, "device_id": d, "device_name": f"#{d}",
                         "ok": False, "output": "", "error": str(exc)} for d in dev_ids]
    return StepResult(idx, all(r.get("ok") for r in results) if results else True, results)


async def run_playbook(pb: PlaybookIn) -> dict:
    steps_out = []
    for idx, step in enumerate(pb.steps):
        r = await run_step(step, idx)
        steps_out.append(r.dict())
        if not r.ok:
            break  # 失败即停（后续步骤跳过）
    return {"name": pb.name or "未命名", "ok": all(s["ok"] for s in steps_out),
            "steps": steps_out}
