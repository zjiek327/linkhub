@echo off
chcp 65001 >nul
title LinkHub
cd /d %~dp0

rem ==== 前置自检：失败时打印原因并 pause 停住，绝不闪退 ====
if not exist "%~dp0linkhub.exe" (
    echo [错误] 当前目录没有 linkhub.exe
    echo        目录: %~dp0
    echo        请先 go build，或把本 bat 放到 exe 同目录
    pause
    exit /b 1
)
netstat -ano | findstr ":8000" | findstr "LISTENING" >nul
if %errorlevel%==0 (
    echo [错误] 8000 端口已被占用（另一个 linkhub 实例在跑）：
    netstat -ano | findstr ":8000" | findstr "LISTENING"
    echo        如需手动模式，先执行: schtasks /End /TN "LinkHub"
    echo        或: taskkill /F /IM linkhub.exe
    pause
    exit /b 1
)

rem ==== 集群 token：必须与 39 服务器一致（不填无法组网）====
set LINKHUB_CLUSTER_TOKEN=在这里填token

rem ==== 集群对端：必须写 LAPTOP 可达的地址（10.218.146.39 被网段 ACL 拦截，10.110.20.149 通）====
set LINKHUB_CLUSTER_PEERS=http://10.110.20.149:8000

rem ==== 远程桌面降分辨率采集：0.5=半分辨率（延迟优先，推荐）====
rem ==== 要全清晰度删除下一行即可 ====
set LINKHUB_DESKTOP_SCALE=0.5

rem ==== 帧率上限（默认24，机器吃力可改15）====
rem set LINKHUB_DESKTOP_FPS=24

rem ==== 运行：日志写入与本 bat 同目录的 start.log ====
echo 日志文件: %~dp0start.log
echo 实时查看可另开窗口执行: powershell Get-Content "%~dp0start.log" -Wait
echo.
linkhub.exe >> "%~dp0start.log" 2>&1
echo.
echo linkhub.exe 已退出（exit code %errorlevel%），日志见 start.log
pause
