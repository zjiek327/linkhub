; 灵枢 LinkHub Windows 安装包（Inno Setup 6）
; 前置：先 build-windows.bat 产出 dist/LinkHubBackend/ 与 frontend/dist/
#define MyAppName "灵枢 LinkHub"
#define MyAppVersion "0.1.0"
#define MyAppPublisher "LinkHub"
#define MyAppExe "LinkHubBackend.exe"

[Setup]
AppId={{8F3A2B1C-4E5D-4A6B-9C0D-1E2F3A4B5C6D}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppPublisher={#MyAppPublisher}
DefaultDirName={autopf}\LinkHub
DefaultGroupName=LinkHub
OutputDir=..\installer
OutputBaseFilename=LinkHubSetup-{#MyAppVersion}
Compression=lzma2/max
SolidCompression=yes
WizardStyle=modern
PrivilegesRequired=lowest
ArchitecturesInstallIn64BitMode=x64

[Languages]
Name: "chinesesimp"; MessagesFile: "compiler:Languages\ChineseSimplified.isl"
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "创建桌面快捷方式"; GroupDescription: "快捷方式："
Name: "startupicon"; Description: "开机自动启动"; GroupDescription: "启动：" ; Flags: unchecked

[Files]
; 后端（PyInstaller onedir 产物）
Source: "dist\LinkHubBackend\*"; DestDir: "{app}\backend"; Flags: ignoreversion recursesubdirs createallsubdirs
; 前端构建产物（已内嵌进后端 app/dist，不重复拷）
; 启动脚本
Source: "scripts\linkhub-tray.bat"; DestDir: "{app}"; Flags: ignoreversion
Source: "scripts\linkhub.bat"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\启动 LinkHub"; Filename: "{app}\linkhub.bat"; IconFilename: "{app}\backend\LinkHubBackend.exe"
Name: "{group}\打开管理界面"; Filename: "http://localhost:8000"
Name: "{group}\卸载 LinkHub"; Filename: "{uninstallexe}"
Name: "{autodesktop}\LinkHub"; Filename: "{app}\linkhub.bat"; Tasks: desktopicon

[Run]
Filename: "{app}\linkhub.bat"; Description: "立即启动 LinkHub"; Flags: nowait postinstall skipifsilent

[Registry]
; 可选开机自启（HKEY_CURRENT_USER，免管理员）
Root: HKCU; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; ValueType: string; \
  ValueName: "LinkHub"; ValueData: """{app}\linkhub.bat"""; Tasks: startupicon

[UninstallRun]
Filename: "taskkill"; Parameters: "/F /IM LinkHubBackend.exe"; Flags: runhidden
