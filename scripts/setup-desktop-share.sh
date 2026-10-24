#!/usr/bin/env bash
# LinkHub 节点桌面共享：Ubuntu 节点侧 VNC 服务端安装/启动辅助
# 用法：bash scripts/setup-desktop-share.sh [--apply]
#   不加 --apply：只探测会话类型并打印建议命令（默认，安全）
#   加 --apply  ：自动安装并启动对应 VNC 服务端（X11 → x11vnc）
# 无桌面（headless）节点不支持桌面共享，本脚本会直接说明并退出。
set -euo pipefail

PORT="${LINKHUB_DESKTOP_PORT:-5900}"
APPLY="${1:-}"

echo "==> 探测图形会话…"
if ls /tmp/.X11-unix/X* >/dev/null 2>&1; then
  SESSION="x11"
  DISPLAY_NUM="$(ls /tmp/.X11-unix/ | grep -oP '^X\K[0-9]+' | head -1)"
elif ls /run/user/*/wayland-* >/dev/null 2>&1; then
  SESSION="wayland"
else
  SESSION="headless"
fi
echo "    会话类型: $SESSION"

if ss -ltn 2>/dev/null | grep -q ":${PORT} "; then
  echo "==> 端口 $PORT 已有服务在监听，VNC 服务端可能已在运行，无需操作。"
  exit 0
fi

case "$SESSION" in
  x11)
    echo "==> X11 会话（DISPLAY=:$DISPLAY_NUM），推荐 x11vnc 共享当前桌面："
    echo
    echo "    sudo apt install -y x11vnc"
    echo "    x11vnc -display :$DISPLAY_NUM -forever -shared -rfbport $PORT -passwd '你的密码' &"
    echo
    if [ "$APPLY" = "--apply" ]; then
      read -rsp "输入 VNC 访问密码: " PW; echo
      sudo apt install -y x11vnc
      nohup x11vnc -display ":$DISPLAY_NUM" -forever -shared -rfbport "$PORT" -passwd "$PW" \
        > ~/.x11vnc.log 2>&1 &
      echo "==> x11vnc 已启动（日志 ~/.x11vnc.log）"
      echo "    建议把上面 nohup 命令加入开机自启（如 systemd user service / 桌面自启动项）"
    fi
    ;;
  wayland)
    echo "==> Wayland 会话："
    echo "    · Ubuntu 24.04+（GNOME 46+）：设置 → 共享 → 桌面共享 → 开启并选 VNC 模式，设置密码即可"
    echo "    · 其他合成器（sway/wlroots 系）：安装 wayvnc 后运行："
    echo "      wayvnc 0.0.0.0 $PORT"
    echo "    （GNOME < 46 的 Wayland 只有 RDP 模式，浏览器无法直连，请改用 X11 会话 + x11vnc）"
    ;;
  headless)
    echo "==> 未检测到图形桌面环境（headless 节点）。"
    echo "    LinkHub v1 桌面共享不支持无桌面节点；如确需，请自行搭建 TigerVNC 虚拟桌面（+xfce4）。"
    exit 1
    ;;
esac
