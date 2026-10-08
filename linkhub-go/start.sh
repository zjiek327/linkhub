#!/usr/bin/env bash
# 灵枢 LinkHub Go 版一键启动脚本（Linux/macOS）
# 用法: ./start.sh [start|stop|restart|status]   默认 start
cd "$(dirname "$0")"

# ---- 本机配置（.env 存在则加载，可用环境变量覆盖）----
[ -f .env ] && . ./.env

# ---- 配置（可用环境变量覆盖）----
export LINKHUB_PORT=${LINKHUB_PORT:-8000}
export LINKHUB_CLUSTER_ENABLED=${LINKHUB_CLUSTER_ENABLED:-true}
export LINKHUB_CLUSTER_TOKEN=${LINKHUB_CLUSTER_TOKEN:-dev-token}
export LINKHUB_NODE_NAME=${LINKHUB_NODE_NAME:-$(hostname)}
export LINKHUB_ADVERTISE_ADDR=${LINKHUB_ADVERTISE_ADDR:-http://$(hostname -I 2>/dev/null | awk '{print $1}'):$LINKHUB_PORT}
export LINKHUB_CLUSTER_PEERS=${LINKHUB_CLUSTER_PEERS:-}
PID_FILE=.run/linkhub.pid
LOG_FILE=linkhub.log

pid() { [ -f "$PID_FILE" ] && ps -p "$(cat "$PID_FILE")" -o pid= 2>/dev/null; }

do_start() {
  if pid; then echo "✓ 已在运行 (pid $(cat "$PID_FILE"))"; exit 0; fi
  # 源码比二进制新（如 git pull 之后）则重新编译
  if [ ! -x ./linkhub ] || [ -n "$(find . -name '*.go' -newer ./linkhub -print -quit 2>/dev/null)" ]; then
    echo "正在编译 linkhub..."
    go build -o linkhub . || { echo "✗ 编译失败"; exit 1; }
  fi
  mkdir -p .run
  nohup ./linkhub > "$LOG_FILE" 2>&1 &
  echo $! > "$PID_FILE"
  sleep 1
  if pid; then
    echo "✓ 灵枢 LinkHub 已启动: http://localhost:$LINKHUB_PORT (pid $(cat "$PID_FILE"))"
    echo "  日志: $LOG_FILE"
  else
    echo "✗ 启动失败，最近日志:"; tail -10 "$LOG_FILE"; exit 1
  fi
}

do_stop() {
  if pid; then kill "$(cat "$PID_FILE")" && rm -f "$PID_FILE" && echo "✓ 已停止"; else echo "未在运行"; fi
}

case "${1:-start}" in
  start)   do_start ;;
  stop)    do_stop ;;
  restart) do_stop; sleep 1; do_start ;;
  status)  pid && echo "运行中 (pid $(cat "$PID_FILE"))，端口 $LINKHUB_PORT" || echo "未运行" ;;
  *) echo "用法: $0 [start|stop|restart|status]"; exit 1 ;;
esac
