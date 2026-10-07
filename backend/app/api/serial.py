"""串口发现与连通性测试 API。"""
import asyncio

from fastapi import APIRouter
from serial.tools import list_ports

from ..schemas import SerialPortInfo, SerialTestIn, SerialTestOut

router = APIRouter(prefix="/api/serial", tags=["serial"])


@router.get("/ports", response_model=list[SerialPortInfo])
async def list_serial_ports(include_virtual: bool = False):
    """枚举本机串口。默认只列 USB 转串口 / Gadget(ttyACM) 等真实可用口，
    include_virtual=true 时连同主板 ttyS* 一起返回。"""
    result = []
    for p in list_ports.comports():
        is_usb = "USB" in (p.hwid or "").upper() or "ttyACM" in p.device
        has_info = bool(p.description and p.description != "n/a")
        if not include_virtual and not (is_usb or has_info):
            continue
        result.append(SerialPortInfo(
            device=p.device,
            description=p.description or "",
            hwid=p.hwid or "",
            is_usb=is_usb,
        ))
    return result


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
