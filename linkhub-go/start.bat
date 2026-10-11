@echo off
rem 灵枢 LinkHub Go 版一键启动（Windows）：双击即可
cd /d "%~dp0"

rem ---- 加载本机 .env 配置（不存在则用默认值）----
rem LINKHUB_CLUSTER_PEERS 支持多行书写：重复出现时自动逗号累加（否则后行会覆盖前行）
if exist .env (
  for /f "usebackq eol=# tokens=1,* delims==" %%a in (".env") do call :loadenv "%%a" "%%b"
)
if not defined LINKHUB_PORT set LINKHUB_PORT=8000
if not defined LINKHUB_CLUSTER_ENABLED set LINKHUB_CLUSTER_ENABLED=true
if not defined LINKHUB_CLUSTER_TOKEN set LINKHUB_CLUSTER_TOKEN=dev-token
if not defined LINKHUB_NODE_NAME set LINKHUB_NODE_NAME=%COMPUTERNAME%

rem ---- 有 Go 就重新编译（保证 git pull 后二进制最新），没有就用已有 exe ----
where go >nul 2>nul
if not errorlevel 1 (
  echo 正在编译 linkhub.exe...
  go build -o linkhub.exe . || (echo 编译失败，尝试使用已有 exe & if not exist linkhub.exe exit /b 1)
) else if not exist linkhub.exe (
  echo 未发现 linkhub.exe 且未安装 Go，请先编译或拷贝二进制
  pause
  exit /b 1
)

start "LinkHub" linkhub.exe
echo 灵枢 LinkHub 已启动: http://localhost:%LINKHUB_PORT%
echo 节点: %LINKHUB_NODE_NAME%  集群: %LINKHUB_CLUSTER_ENABLED%
echo 关闭本窗口不影响运行；停止请在任务管理器结束 linkhub.exe
timeout /t 3
goto :eof

:loadenv
rem %~1=key %~2=value（CRLF 已由 for /f 处理）
if "%~2"=="" goto :eof
if /i "%~1"=="LINKHUB_CLUSTER_PEERS" (
  if defined LINKHUB_CLUSTER_PEERS (
    set "LINKHUB_CLUSTER_PEERS=%LINKHUB_CLUSTER_PEERS%,%~2"
  ) else (
    set "LINKHUB_CLUSTER_PEERS=%~2"
  )
) else (
  if not defined %~1 set "%~1=%~2"
)
goto :eof
