"""跨平台一键启动器（纯 Python 版）。

替代 bash linkhub.sh，在 Windows/macOS/Linux 上行为一致。
用法：
  python scripts/linkhub.py start [--frontend-port 5173] [--port 8000] [--no-frontend]
  python scripts/linkhub.py stop
  python scripts/linkhub.py status
  python scripts/linkhub.py restart
"""
import argparse
import os
import signal
import subprocess
import sys
import time
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
BACKEND = ROOT / "backend"
FRONTEND = ROOT / "frontend"
RUN_DIR = ROOT / ".run"
LOG_DIR = ROOT / "logs"
IS_WINDOWS = sys.platform.startswith("win")

RUN_DIR.mkdir(exist_ok=True)
LOG_DIR.mkdir(exist_ok=True)


def banner():
    print(r"""
        _______________
       /  灵枢 LinkHub \
      |   ___________   |
      |  /  ^     ^  \  |     连连已经准备好了！
      | |   •  ω  •   | |
       \ \   ‿‿‿    / /
        \ \_______/ /
       ~/|  |   |  |\~
      ~ / |  |   |  | \ ~
""")


def venv_python() -> Path:
    if IS_WINDOWS:
        return BACKEND / ".venv" / "Scripts" / "python.exe"
    return BACKEND / ".venv" / "bin" / "python"


def ensure_venv() -> None:
    py = venv_python()
    if py.exists():
        return
    print("🐙 创建 Python 虚拟环境…")
    subprocess.run([sys.executable, "-m", "venv", str(BACKEND / ".venv")], check=True)


def ensure_backend_deps() -> None:
    py = venv_python()
    marker = RUN_DIR / ".backend-deps"
    req = BACKEND / "requirements.txt"
    if not marker.exists() or marker.stat().st_mtime < req.stat().st_mtime:
        print("🐙 安装后端依赖…")
        subprocess.run([str(py), "-m", "pip", "install", "--quiet", "--upgrade", "pip"], check=True)
        subprocess.run([str(py), "-m", "pip", "install", "--quiet", "-r", str(req)], check=True)
        marker.touch()


def ensure_frontend_deps() -> None:
    if not (FRONTEND / "node_modules").exists():
        print("🐙 安装前端依赖（首次约 1~2 分钟）…")
        subprocess.run(["npm", "install", "--silent"], cwd=FRONTEND, check=True,
                       shell=IS_WINDOWS)


def pid_file(name: str) -> Path:
    return RUN_DIR / f"{name}.pid"


def is_running(name: str) -> int | None:
    f = pid_file(name)
    if not f.exists():
        return None
    try:
        pid = int(f.read_text().strip())
        os.kill(pid, 0)
        return pid
    except (ProcessLookupError, ValueError, PermissionError):
        return None


def spawn(name: str, cmd: list[str], cwd: Path, env: dict) -> None:
    log = open(LOG_DIR / f"{name}.log", "a", encoding="utf-8")
    flags = 0
    if IS_WINDOWS:
        flags = subprocess.CREATE_NEW_PROCESS_GROUP | subprocess.DETACHED_PROCESS
    proc = subprocess.Popen(
        cmd, cwd=cwd, env={**os.environ, **env}, stdout=log, stderr=log,
        stdin=subprocess.DEVNULL, creationflags=flags if IS_WINDOWS else 0,
        start_new_session=not IS_WINDOWS)
    pid_file(name).write_text(str(proc.pid))


def kill_proc(name: str) -> None:
    pid = is_running(name)
    if not pid:
        print(f"🐙 {name} 未在运行")
        pid_file(name).unlink(missing_ok=True)
        return
    try:
        if IS_WINDOWS:
            subprocess.run(["taskkill", "/F", "/T", "/PID", str(pid)],
                           capture_output=True)
        else:
            os.killpg(os.getpgid(pid), signal.SIGTERM)
            time.sleep(1.5)
            if is_running(name):
                os.killpg(os.getpgid(pid), signal.SIGKILL)
    except Exception:
        pass
    pid_file(name).unlink(missing_ok=True)
    print(f"✓ {name} 已停止")


def wait_health(url: str, timeout: int = 30) -> bool:
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            if urllib.request.urlopen(url, timeout=1).status == 200:
                return True
        except Exception:
            time.sleep(0.5)
    return False


def load_env() -> dict:
    env = {}
    env_file = BACKEND / ".env"
    if env_file.exists():
        for line in env_file.read_text(encoding="utf-8").splitlines():
            line = line.strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            if any(c in line for c in "[]{}'\""):
                continue  # 复杂值交给后端 pydantic 自己读
            k, v = line.split("=", 1)
            env[k.strip()] = v.strip()
    return env


def cmd_start(args) -> None:
    banner()
    ensure_venv()
    ensure_backend_deps()
    if not args.no_frontend:
        ensure_frontend_deps()
    env = load_env()
    if env:
        print(f"🐙 已加载 {BACKEND / '.env'}")

    backend_env = {**env, "PYTHONUNBUFFERED": "1"}
    if is_running("backend"):
        print(f"✓ 后端已在运行（PID {is_running('backend')}）")
    else:
        spawn("backend", [str(venv_python()), "-m", "uvicorn", "app.main:app",
                          "--host", "0.0.0.0", "--port", str(args.port)],
              BACKEND, backend_env)
    if wait_health(f"http://127.0.0.1:{args.port}/api/health"):
        print(f"✓ 后端就绪 → http://127.0.0.1:{args.port}（API 文档 /docs）")
    else:
        print(f"✗ 后端启动失败，日志：{LOG_DIR / 'backend.log'}")
        sys.exit(1)

    if not args.no_frontend:
        if is_running("frontend"):
            print(f"✓ 前端已在运行（PID {is_running('frontend')}）")
        else:
            npm = ["npm", "run", "dev", "--", "--host", "--port", str(args.frontend_port)]
            spawn("frontend", npm, FRONTEND, env)
        time.sleep(4)
        print(f"✓ 前端就绪 → http://127.0.0.1:{args.frontend_port}")
    if env.get("LINKHUB_CLUSTER_ENABLED") == "true":
        print(f"✓ 集群模式：{env.get('LINKHUB_NODE_NAME', '?')} @ {env.get('LINKHUB_ADVERTISE_ADDR', '?')}")


def cmd_stop(_args) -> None:
    kill_proc("backend")
    kill_proc("frontend")


def cmd_status(_args) -> None:
    for name in ("backend", "frontend"):
        pid = is_running(name)
        print(f"{'✓' if pid else '✗'} {name} {'运行中 PID ' + str(pid) if pid else '未运行'}")


def main() -> None:
    ap = argparse.ArgumentParser(description="灵枢 LinkHub 一键启动器（跨平台）")
    ap.add_argument("cmd", choices=["start", "stop", "restart", "status"])
    ap.add_argument("--port", type=int, default=8000)
    ap.add_argument("--frontend-port", type=int, default=5173)
    ap.add_argument("--no-frontend", action="store_true")
    args = ap.parse_args()
    {"start": cmd_start, "stop": cmd_stop, "status": cmd_status,
     "restart": lambda a: (cmd_stop(a), time.sleep(1), cmd_start(a))}[args.cmd](args)


if __name__ == "__main__":
    main()
