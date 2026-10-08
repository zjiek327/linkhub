"""系统信息 API：本机网卡列表 + 发现网络选择。"""
import ipaddress
import json
import socket

import psutil
from fastapi import APIRouter, Depends
from sqlalchemy.ext.asyncio import AsyncSession

from ..auth import require_admin, require_operator
from ..database import get_db
from ..models import Meta

router = APIRouter(prefix="/api/system", tags=["system"])


def _broadcast_for(ip: str, netmask: str) -> str:
    try:
        return str(ipaddress.ip_network(f"{ip}/{netmask}", strict=False).broadcast_address)
    except ValueError:
        return ""


@router.get("/interfaces")
async def list_interfaces():
    """列出本机所有 UP 的网卡及其 IPv4/广播地址。"""
    stats = psutil.net_if_stats()
    addrs = psutil.net_if_addrs()
    out = []
    for name, snics in addrs.items():
        st = stats.get(name)
        if st and not st.isup:
            continue
        if name in ("lo", "lo0", "Loopback Pseudo-Interface 1"):
            continue
        for snic in snics:
            if snic.family != socket.AF_INET:
                continue
            ip = snic.address
            if ip.startswith("127."):
                continue
            brd = getattr(snic, "broadcast", None) or _broadcast_for(ip, snic.netmask or "255.255.255.0")
            out.append({
                "name": name,
                "ip": ip,
                "netmask": snic.netmask or "",
                "broadcast": brd,
                "is_up": st.isup if st else True,
            })
    return out


@router.get("/beacon-interfaces", dependencies=[Depends(require_operator)])
async def get_beacon_interfaces(db: AsyncSession = Depends(get_db)):
    """当前 beacon 从哪些网卡广播（空列表=全部）。"""
    row = await db.get(Meta, "beacon_interfaces")
    return {"interfaces": json.loads(row.value) if row and row.value else []}


@router.post("/beacon-interfaces", dependencies=[Depends(require_operator)])
async def set_beacon_interfaces(body: dict, db: AsyncSession = Depends(get_db)):
    """保存网卡选择（存广播地址列表），beacon 下轮生效。"""
    selected = body.get("broadcasts", [])
    row = await db.get(Meta, "beacon_interfaces")
    if row is None:
        row = Meta(key="beacon_interfaces", value="[]")
        db.add(row)
    row.value = json.dumps(selected)
    await db.commit()
    return {"interfaces": selected}



def _broadcast_for(ip: str, netmask: str) -> str:
    try:
        return str(ipaddress.ip_network(f"{ip}/{netmask}", strict=False).broadcast_address)
    except ValueError:
        return ""


@router.get("/interfaces")
async def list_interfaces():
    """列出本机所有 UP 的网卡及其 IPv4/广播地址。"""
    stats = psutil.net_if_stats()
    addrs = psutil.net_if_addrs()
    out = []
    for name, snics in addrs.items():
        st = stats.get(name)
        if st and not st.isup:
            continue
        if name in ("lo", "lo0", "Loopback Pseudo-Interface 1"):
            continue
        for snic in snics:
            if snic.family != socket.AF_INET:
                continue
            ip = snic.address
            if ip.startswith("127."):
                continue
            brd = getattr(snic, "broadcast", None) or _broadcast_for(ip, snic.netmask or "255.255.255.0")
            out.append({
                "name": name,
                "ip": ip,
                "netmask": snic.netmask or "",
                "broadcast": brd,
                "is_up": st.isup if st else True,
            })
    return out
