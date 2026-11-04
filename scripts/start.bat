@echo off
chcp 65001 >nul
title LinkHub
cd /d %~dp0

rem ==== 集群 token：必须与 39 服务器一致（不填无法组网）====
set LINKHUB_CLUSTER_TOKEN=在这里填token

rem ==== 远程桌面降分辨率采集：0.5=半分辨率（延迟优先，推荐）====
rem ==== 要全清晰度删除下一行即可 ====
set LINKHUB_DESKTOP_SCALE=0.5

rem ==== 帧率上限（默认24，机器吃力可改15）====
rem set LINKHUB_DESKTOP_FPS=24

linkhub.exe
pause
