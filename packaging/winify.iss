; Inno Setup script for the winify GUI installer.
;
; Build the payload first (packaging\build-installer.ps1), then compile:
;   iscc packaging\winify.iss
;
; The installer places winify.exe and runs install.ps1, which installs and
; starts the Windows service. The service provisions the host on first run.

#define AppName "winify"
#define AppVersion "0.1.0"

[Setup]
AppId={{8E2F1C2A-6B7D-4C1E-9A3B-2D5F6A7B8C90}
AppName={#AppName}
AppVersion={#AppVersion}
AppPublisher=winify
DefaultDirName={autopf}\winify
DisableProgramGroupPage=yes
OutputDir=dist
OutputBaseFilename=winify-setup
Compression=lzma2
SolidCompression=yes
PrivilegesRequired=admin
ArchitecturesInstallIn64BitMode=x64compatible
WizardStyle=modern
UninstallDisplayName=winify (DevOps Control Center)

[Files]
Source: "payload\winify.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "payload\install.ps1"; DestDir: "{app}"; Flags: ignoreversion

[Run]
Filename: "powershell.exe"; \
  Parameters: "-NoProfile -ExecutionPolicy Bypass -File ""{app}\install.ps1"" -Source ""{app}\winify.exe"" -Config ""{commonappdata}\winify\config.yaml"""; \
  StatusMsg: "Installing and starting the winify service..."; \
  Flags: runhidden waituntilterminated

[UninstallRun]
Filename: "powershell.exe"; \
  Parameters: "-NoProfile -Command ""Stop-Service winify -Force -ErrorAction SilentlyContinue; sc.exe delete winify"""; \
  Flags: runhidden

[UninstallDelete]
Type: filesandordirs; Name: "{app}"
