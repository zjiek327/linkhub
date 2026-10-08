# Windows 安装包打包指南

把 LinkHub 打成双击安装的 Windows 安装包（免 Python/Node 环境）。

## 架构

```
LinkHubSetup-x.y.z.exe （Inno Setup）
  └─ Program Files\LinkHub\
       ├─ backend\LinkHubBackend.exe   （PyInstaller 打的独立后端，含内嵌前端）
       ├─ linkhub.bat                  （交互启动，带控制台日志）
       └─ linkhub-tray.bat             （静默启动，开机自启用）
  用户数据：%APPDATA%\LinkHub\（数据库、配置、会话日志）
  访问：浏览器打开 http://localhost:8000
```

## 一次性环境准备（构建机）

1. **Python 3.12+**：https://www.python.org/downloads/ （勾选 Add to PATH）
2. **Node.js 20+**：https://nodejs.org/ （LTS）
3. **Inno Setup 6**：https://jrsoftware.org/isdl.php （装完把 `iscc` 加进 PATH）

## 构建

在项目根目录双击或命令行执行：

```bat
build-windows.bat
```

产出：`backend\installer\LinkHubSetup-0.1.0.exe`

## 安装后的行为

- 桌面/开始菜单有「启动 LinkHub」图标
- 可选「开机自动启动」（静默模式）
- 浏览器打开 http://localhost:8000 即完整应用（后端已内嵌前端）
- 卸载时会自动结束运行中的 LinkHub 进程

## 常见问题

**Q: 安装后打不开 8000 端口？**
A: 可能被防火墙拦了首次弹窗（允许即可），或端口被占。换端口：编辑 `%APPDATA%\LinkHub\.env` 加 `LINKHUB_PORT=9000`，重启。

**Q: 串口 COM 口不出现？**
A: 设备管理器确认 CH340/CP2102 驱动已装（Windows 10+ 一般自带）。连接配置的端口下拉会列出 `COM3` 等。

**Q: 企业域控机器限制安装？**
A: `installer.iss` 里 `PrivilegesRequired=lowest`，装到用户目录，不需要管理员权限。

**Q: 怎么升级？**
A: 直接装新版（同目录覆盖安装），数据库和配置保留在 `%APPDATA%\LinkHub`。
