@echo off
rem ============================================
rem  灵枢 LinkHub Windows 安装包一键构建
rem  前置：Python 3.12+ 与 Node.js 20+ 已安装且在 PATH
rem ============================================
setlocal
cd /d %~dp0\..    rem 项目根目录

echo [1/4] 构建前端…
pushd frontend
call npm install --silent || goto :fail
call npm run build || goto :fail
popd

echo [2/4] 准备后端虚拟环境…
if not exist backend\.venv (
  python -m venv backend\.venv || goto :fail
)
call backend\.venv\Scripts\activate.bat
pip install --quiet --upgrade pip
pip install --quiet -r backend\requirements.txt
pip install --quiet pyinstaller

echo [3/4] PyInstaller 打包后端…
pushd backend
pyinstaller --noconfirm --clean linkhub-backend.spec || goto :fail
popd

echo [4/4] Inno Setup 出安装包…
where iscc > nul 2>&1
if errorlevel 1 (
  echo 未找到 Inno Setup。请安装：https://jrsoftware.org/isdl.php
  echo 装好后重跑本脚本，或手动编译 backend\installer.iss
  goto :done
)
iscc backend\installer.iss || goto :fail
echo 安装包产出：backend\installer\LinkHubSetup-0.1.0.exe
:done
endlocal
exit /b 0

:fail
echo 构建失败，见上方错误。
endlocal
exit /b 1
