@echo off
chcp 65001 >nul
title LinkHub
cd /d %~dp0

rem ==== 前置自检：失败时打印原因并 pause 停住，绝不闪退 ====
rem exe 定位：bat 同目录优先，其次仓库布局的上级 backend\（go build 产物所在）
set "EXE=%~dp0linkhub.exe"
if not exist "%EXE%" set "EXE=%~dp0..\backend\linkhub.exe"
if not exist "%EXE%" (
    echo [错误] 找不到 linkhub.exe，已查找：
    echo        %~dp0linkhub.exe
    echo        %~dp0..\backend\linkhub.exe
    echo        请先在 backend 目录执行 go build
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
"%EXE%" >> "%~dp0start.log" 2>&1
echo.
echo linkhub.exe 已退出（exit code %errorlevel%），日志见 %~dp0start.log
pause
