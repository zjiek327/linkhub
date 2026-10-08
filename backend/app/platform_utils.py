"""跨平台工具：操作系统检测、串口设备识别、广播地址计算。"""
import ipaddress
import os
import platform
import socket

OS = platform.system()           # Linux / Windows / Darwin
IS_LINUX = OS == "Linux"
IS_WINDOWS = OS == "Windows"
IS_MAC = OS == "Darwin"


def platform_serial_example() -> str:
    """给前端的串口示例值，按平台给。"""
    if IS_WINDOWS:
        return "COM3"
    if IS_MAC:
        return "/dev/cu.usbserial-XXXX"
    return "/dev/ttyUSB0"


def is_real_serial(device: str, description: str, hwid: str) -> bool:
    """区分真实可用串口 vs 系统虚拟口，过滤规则按平台。"""
    if IS_WINDOWS:
        # Windows 下 list_ports 描述常带 "USB Serial Port (COMx)" / "USB-SERIAL"
        return "usb" in description.lower() or "usb" in hwid.lower() or "com" in device.lower()
    if IS_MAC:
        return device.startswith("/dev/cu.usb") or "usb" in hwid.lower()
    # Linux
    return "USB" in hwid.upper() or "ttyACM" in device or bool(description and description != "n/a")


def broadcast_addrs() -> list[str]:
    """所有本地接口的定向广播地址（跨平台实现）。
    Linux 用 ioctl 精确；其他平台用主机名解析 + /24 推广播近似（够用于发现）。"""
    addrs = {"255.255.255.255"}
    if IS_LINUX:
        try:
            import fcntl
            import struct
            for _, ifname in socket.if_nameindex():
                try:
                    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
                    brd = fcntl.ioctl(s.fileno(), 0x8919, struct.pack("256s", ifname.encode()))[20:24]
                    s.close()
                    addrs.add(socket.inet_ntoa(brd))
                except OSError:
                    continue
        except ImportError:
            pass
        return sorted(a for a in addrs if a != "0.0.0.0")
    # 跨平台近似：本机每个 IPv4 地址按 /24 推定向广播
    try:
        for ip in socket.gethostbyname_ex(socket.gethostname())[2]:
            if ip.startswith("127."):
                continue
            net = ipaddress.ip_network(f"{ip}/24", strict=False)
            addrs.add(str(net.broadcast_address))
    except OSError:
        pass
    return sorted(addrs)


def sqlite_url(path) -> str:
    """跨平台 SQLite URL（Windows 盘符/反斜杠兼容）。"""
    from pathlib import Path
    p = Path(path).absolute().as_posix()
    return f"sqlite+aiosqlite:///{p}"
