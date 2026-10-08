@echo off
rem 灵枢 LinkHub 一键启动（Windows）
rem 由安装包部署到安装目录，桌面快捷方式指向这里
cd /d "%~dp0backend"
if exist "%APPDATA%\LinkHub\.env" (
  rem 用户数据目录的配置优先
  set LINKHUB_DATA_DIR=%APPDATA%\LinkHub
)
start "" LinkHubBackend.exe
timeout /t 3 /nobreak > nul
start "" "http://localhost:8000"
