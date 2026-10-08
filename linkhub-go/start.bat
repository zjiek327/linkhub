@echo off
rem 灵枢 LinkHub Go 版一键启动（Windows）：双击即可
cd /d "%~dp0"
if not exist linkhub.exe (
  echo 未发现 linkhub.exe，请先执行: GOOS=windows GOARCH=amd64 go build -o linkhub.exe .
  pause
  exit /b 1
)
if not defined LINKHUB_PORT set LINKHUB_PORT=8000
if not defined LINKHUB_CLUSTER_ENABLED set LINKHUB_CLUSTER_ENABLED=true
if not defined LINKHUB_CLUSTER_TOKEN set LINKHUB_CLUSTER_TOKEN=dev-token
if not defined LINKHUB_NODE_NAME set LINKHUB_NODE_NAME=%COMPUTERNAME%
start "LinkHub" linkhub.exe
echo 灵枢 LinkHub 已启动: http://localhost:%LINKHUB_PORT%
echo 关闭本窗口不影响运行；停止请在任务管理器结束 linkhub.exe
timeout /t 3
