"""串口发现与连通性测试 API。"""
import asyncio

from fastapi import APIRouter
from serial.tools import list_ports

from ..cluster.state import state
from ..schemas import SerialPortInfo, SerialTestIn, SerialTestOut

router = APIRouter(prefix="/api/serial", tags=["serial"])


@router.get("/ports", response_model=list[SerialPortInfo])
async def list_serial_ports(include_virtual: bool = False):
    """枚举串口。默认只列 USB 转串口 / Gadget(ttyACM) 等真实可用口；
    集群模式下聚合同步各节点上报的资源。"""
    result = [_port_info(p, include_virtual) for p in list_ports.comports()]
    result = [p for p in result if p is not None]
    if state.enabled:
        for peer in state.peers.values():
            if peer.status != "online":
                continue
            for sp in (peer.resources or {}).get("serial_ports", []):
                result.append(SerialPortInfo(
                    device=sp["device"], description=sp.get("description", ""),
                    hwid="", is_usb=True, node=peer.name, node_id=peer.node_id,
                ))
    return result


from ..platform_utils import is_real_serial


def _port_info(p, include_virtual: bool) -> SerialPortInfo | None:
    real = is_real_serial(p.device, p.description or "", p.hwid or "")
    if not include_virtual and not real:
        return None
    return SerialPortInfo(device=p.device, description=p.description or "",
                          hwid=p.hwid or "", is_usb=real)


@router.post("/test", response_model=SerialTestOut)
async def test_serial(body: SerialTestIn):
    """试打开串口并短读，验证参数可用、看看有没有 banner 输出。"""
    import serial_asyncio

    writer = None
    try:
        reader, writer = await serial_asyncio.open_serial_connection(
            url=body.port, baudrate=body.baudrate, bytesize=body.bytesize,
            parity=body.parity, stopbits=body.stopbits, timeout=0,
        )
        await asyncio.sleep(body.probe_ms / 1000)
        data = await asyncio.wait_for(reader.read(4096), timeout=0.5)
        return SerialTestOut(ok=True, banner=data.decode(errors="replace"))
    except TimeoutError:
        return SerialTestOut(ok=True, banner="")  # 能打开但无输出，也正常
    except Exception as exc:
        return SerialTestOut(ok=False, error=str(exc))
    finally:
        if writer is not None:
            writer.close()


@router.get("/ble/scan")
async def ble_scan(timeout: float = 8.0):
    """BLE 扫描发现（P3）。无蓝牙适配器的环境返回空列表。"""
    from ..connectors.ble_connector import scan_ble
    try:
        return await scan_ble(min(timeout, 20))
    except Exception as exc:
        return [{"address": "", "name": f"扫描失败: {exc}"}]
