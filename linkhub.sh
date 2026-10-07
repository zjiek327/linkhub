#!/usr/bin/env bash
# 🐙 灵枢 LinkHub 一键启动脚本
#
# 用法：
#   ./linkhub.sh start            # 启动后端 + 前端（自动装依赖）
#   ./linkhub.sh stop             # 停止
#   ./linkhub.sh restart          # 重启
#   ./linkhub.sh status           # 查看状态
#   ./linkhub.sh logs             # 跟踪日志
#
# 集群模式：在 backend/.env 里配置（存在即生效）：
#   LINKHUB_CLUSTER_ENABLED=true
#   LINKHUB_CLUSTER_TOKEN=你的令牌
#   LINKHUB_NODE_NAME=这台机器的名字
#   LINKHUB_ADVERTISE_ADDR=http://本机IP:8000
#
# 环境变量：LINKHUB_PORT（后端端口，默认 8000）
#           LINKHUB_FRONTEND_PORT（前端端口，默认 5173）
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BACKEND="$ROOT/backend"
FRONTEND="$ROOT/frontend"
RUN_DIR="$ROOT/.run"
LOG_DIR="$ROOT/logs"
BACKEND_PORT="${LINKHUB_PORT:-8000}"
FRONTEND_PORT="${LINKHUB_FRONTEND_PORT:-5173}"

mkdir -p "$RUN_DIR" "$LOG_DIR"

log()  { echo -e "\033[1;36m🐙\033[0m $*"; }
ok()   { echo -e "\033[1;32m✓\033[0m $*"; }
err()  { echo -e "\033[1;31m✗ $*\033[0m" >&2; }

banner() {
  cat <<'EOF'
        _______________
       /  灵枢 LinkHub \
      |   ___________   |
      |  /  ^     ^  \  |     连连已经准备好了！
      | |   •  ω  •   | |
       \ \   ‿‿‿    / /
        \ \_______/ /
       ~/|  |   |  |\~
      ~ / |  |   |  | \ ~
EOF
}

# ---------- 环境检查与依赖 ----------
setup_backend() {
  if [ ! -x "$BACKEND/.venv/bin/python" ]; then
    log "创建 Python 虚拟环境..."
    if python3 -c 'import sys; sys.exit(0 if sys.version_info >= (3,11) else 1)' 2>/dev/null; then
      python3 -m venv "$BACKEND/.venv"
    elif command -v uv >/dev/null 2>&1; then
      uv venv --python 3.12 "$BACKEND/.venv"
    else
      err "系统 Python 太老（需 ≥3.11）且未安装 uv。
      二选一：
        uv:      curl -LsSf https://astral.sh/uv/install.sh | sh
        deadsnakes: sudo apt install python3.12 python3.12-venv"
      exit 1
    fi
  fi

  # 依赖有变化才重装（以 requirements.txt mtime 为准）
  local marker="$RUN_DIR/.backend-deps"
  if [ ! -f "$marker" ] || [ "$BACKEND/requirements.txt" -nt "$marker" ]; then
    log "安装后端依赖..."
    "$BACKEND/.venv/bin/pip" install --quiet --upgrade pip
    "$BACKEND/.venv/bin/pip" install --quiet -r "$BACKEND/requirements.txt"
    touch "$marker"
  fi
  ok "后端依赖就绪"
}

setup_frontend() {
  if ! command -v npm >/dev/null 2>&1; then
    err "未找到 npm（需 Node.js ≥ 18）。
      安装: curl -fsSL https://fnm.vercel.app/install | bash && fnm install 22"
    exit 1
  fi
  if [ ! -d "$FRONTEND/node_modules" ] || [ "$FRONTEND/package.json" -nt "$RUN_DIR/.frontend-deps" ] 2>/dev/null; then
    log "安装前端依赖（首次约 1~2 分钟）..."
    (cd "$FRONTEND" && npm install --silent)
    touch "$RUN_DIR/.frontend-deps"
  fi
  ok "前端依赖就绪"
}

# ---------- 进程管理 ----------
is_running() {  # $1=pidfile
  [ -f "$1" ] && kill -0 "$(cat "$1")" 2>/dev/null
}

start_one() {  # $1=name $2=pidfile $3=logfile $4=cmd...
  local name="$1" pidfile="$2" logfile="$3"; shift 3
  if is_running "$pidfile"; then
    ok "$name 已在运行（PID $(cat "$pidfile")）"
    return 0
  fi
  # setsid 独立进程组：停止时杀整组，npm→vite 子进程不残留
  nohup setsid "$@" >"$logfile" 2>&1 &
  echo $! >"$pidfile"
}

wait_health() {  # $1=url
  for _ in $(seq 1 30); do
    curl -sf "$1" >/dev/null 2>&1 && return 0
    sleep 1
  done
  return 1
}

cmd_start() {
  banner
  setup_backend
  setup_frontend

  # 集群配置：存在 backend/.env 则加载
  if [ -f "$BACKEND/.env" ]; then
    set -a; source "$BACKEND/.env"; set +a
    log "已加载 $BACKEND/.env"
  fi

  log "启动后端（端口 $BACKEND_PORT）..."
  (cd "$BACKEND" && start_one "后端" "$RUN_DIR/backend.pid" "$LOG_DIR/backend.log" \
    ./.venv/bin/python -m uvicorn app.main:app --host 0.0.0.0 --port "$BACKEND_PORT")

  if wait_health "http://127.0.0.1:$BACKEND_PORT/api/health"; then
    ok "后端就绪 → http://127.0.0.1:$BACKEND_PORT（API 文档 /docs）"
  else
    err "后端启动失败，日志：$LOG_DIR/backend.log"; tail -5 "$LOG_DIR/backend.log"; exit 1
  fi

  log "启动前端（端口 $FRONTEND_PORT，被占用会自动顺延）..."
  (cd "$FRONTEND" && start_one "前端" "$RUN_DIR/frontend.pid" "$LOG_DIR/frontend.log" \
    npm run dev -- --host --port "$FRONTEND_PORT")
  sleep 4
  local url
  url=$(grep -oE "http://[0-9.]+:[0-9]+/" "$LOG_DIR/frontend.log" | head -1 || true)
  ok "前端就绪 → ${url:-http://127.0.0.1:$FRONTEND_PORT}"

  if [ "${LINKHUB_CLUSTER_ENABLED:-}" = "true" ]; then
    ok "集群模式：${LINKHUB_NODE_NAME:-未命名} @ ${LINKHUB_ADVERTISE_ADDR:-未设置}"
  fi
  echo
  log "停止: $0 stop    日志: $0 logs"
}

cmd_stop() {
  for name in backend frontend; do
    local pidfile="$RUN_DIR/$name.pid"
    if is_running "$pidfile"; then
      local pid; pid=$(cat "$pidfile")
      kill -TERM -- "-$pid" 2>/dev/null || kill "$pid" 2>/dev/null || true
      # 优雅退出最多等 5 秒（uvicorn 会被长连 WS 拖住），超时强杀
      for _ in $(seq 1 50); do
        kill -0 "$pid" 2>/dev/null || break
        sleep 0.1
      done
      if kill -0 "$pid" 2>/dev/null; then
        kill -KILL -- "-$pid" 2>/dev/null || kill -KILL "$pid" 2>/dev/null || true
      fi
      rm -f "$pidfile"
      ok "$name 已停止"
    else
      log "$name 未在运行"
      rm -f "$pidfile"
    fi
  done
}

cmd_status() {
  for name in backend frontend; do
    if is_running "$RUN_DIR/$name.pid"; then
      ok "$name 运行中（PID $(cat "$RUN_DIR/$name.pid")）"
    else
      err "$name 未运行"
    fi
  done
}

cmd_logs() {
  tail -f "$LOG_DIR/backend.log" "$LOG_DIR/frontend.log"
}

case "${1:-start}" in
  start)   cmd_start ;;
  stop)    cmd_stop ;;
  restart) cmd_stop; sleep 1; cmd_start ;;
  status)  cmd_status ;;
  logs)    cmd_logs ;;
  *) echo "用法: $0 {start|stop|restart|status|logs}"; exit 1 ;;
esac
