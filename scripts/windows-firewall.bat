@echo off
chcp 65001 >nul
rem 灵枢 LinkHub Windows 防火墙放行（需要以管理员身份运行）：
rem 症状——Ubuntu 能发现本机但加入失败 / 本机发现不了其他节点，
rem 多为防火墙拦截了入站 TCP 8000（集群握手/API）与 UDP 37890（节点发现信标）。
net session >nul 2>nul || (echo 请右键“以管理员身份运行”本脚本 & pause & exit /b 1)

set PORT=%LINKHUB_PORT%
if not defined PORT set PORT=8000
set DISC=%LINKHUB_DISCOVERY_PORT%
if not defined DISC set DISC=37890

netsh advfirewall firewall add rule name="LinkHub TCP %PORT%" dir=in action=allow protocol=TCP localport=%PORT%
netsh advfirewall firewall add rule name="LinkHub Discovery UDP %DISC%" dir=in action=allow protocol=UDP localport=%DISC%

echo 已放行：入站 TCP %PORT%（LinkHub API/集群握手）、UDP %DISC%（节点发现）
echo 如需撤销：netsh advfirewall firewall delete rule name="LinkHub TCP %PORT%" ^& netsh advfirewall firewall delete rule name="LinkHub Discovery UDP %DISC%"
pause
