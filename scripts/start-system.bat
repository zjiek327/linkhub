@echo off
chcp 65001 >nul
title LinkHub (SYSTEM 模式)
cd /d %~dp0

rem 一键启动 SYSTEM 实例（模式B：可远程解锁锁屏）
rem 需要管理员权限（控制 SYSTEM 计划任务）
net session >nul 2>&1
if %errorlevel% neq 0 (
    echo [错误] 本脚本需要管理员权限——请右键"以管理员身份运行"
    pause
    exit /b 1
)

rem 清场：杀掉手动实例（若有），停掉旧 SYSTEM 任务（若在跑），确保独占 8000
taskkill /F /IM linkhub.exe >nul 2>&1
schtasks /End /TN "LinkHub" >nul 2>&1
timeout /t 1 /nobreak >nul

schtasks /Run /TN "LinkHub"
if %errorlevel% neq 0 (
    echo [错误] 任务启动失败——任务未注册？请先执行:
    echo   schtasks /Create /F /TN "LinkHub" /XML scripts\windows-linkhub-system.xml
    pause
    exit /b 1
)
echo ✓ 已启动 SYSTEM 实例
echo   日志: C:\linkhub\linkhub.log（run-as-system.bat 所在目录）
echo   验证: 浏览器开 http://10.110.50.3:8000 或 39 集群页看 LAPTOP 在线
pause
