@echo off
rem 灵枢 LinkHub 静默启动（无控制台窗口，开机自启用）
cd /d "%~dp0backend"
if exist "%APPDATA%\LinkHub" set LINKHUB_DATA_DIR=%APPDATA%\LinkHub
start "" /min LinkHubBackend.exe
