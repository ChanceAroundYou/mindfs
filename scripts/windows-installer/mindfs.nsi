; MindFS Windows 安装器（NSIS 3 / Unicode）
;
; 设计约束（每条都对应一次真实故障）：
;
;  1. per-user 安装（RequestExecutionLevel user）。升级由自动更新在用户会话里静默执行，
;     整包提权会让每一次 /S 都弹 UAC —— 那等于没有自动更新。
;  2. 但注册 S4U 计划任务**必须**管理员令牌。所以只有「首次安装 / 迁移旧版」这一步
;     用 ShellExecute(runas) 拉起随包 exe 的 --register-service，其余全部免提权。
;  3. 节点名在安装完成之后问（自定义页放在 INSTFILES 之后）：查重要调用刚装好的
;     mindfs.exe，放在拷贝之前只能靠把整个 exe 再嵌一份。
;  4. 停服务只走 schtasks /end —— 它由 Task Scheduler 服务执行，不受跨会话 ACL 限制；
;     直接 taskkill 在受限令牌下会「拒绝访问」并中止整个安装（2026-11-25 的根因）。
;     只有旧版自解压安装（没有计划任务）才退回按进程名强杀。
;  5. 卸载器必须能反复跑：任务不存在、进程不在、PATH 里没有条目都不算失败。
;
; 构建：scripts/build-windows-installer.sh（Linux 上 makensis 交叉构建）。

Unicode true

!include "MUI2.nsh"
!include "LogicLib.nsh"
!include "FileFunc.nsh"
!include "nsDialogs.nsh"

!define APP_NAME "MindFS"
!define APP_EXE "mindfs.exe"
!define APP_REG_KEY "Software\MindFS"
!define UNINST_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\MindFS"
!define TASK_NAME "mindfs"
; 自动更新由这个旁路任务执行：服务自己拉起安装包会跟着服务的 job 一起被终结。
!define UPDATE_TASK_NAME "mindfs-update"
!define WEBVIEW2_GUID "{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}"

!ifndef VERSION
  !define VERSION "dev"
!endif
!ifndef VERSION4
  !define VERSION4 "0.0.0.0"
!endif
!ifndef OUTFILE
  !define OUTFILE "mindfs-setup.exe"
!endif
!ifndef PAYLOAD
  !define PAYLOAD "payload"
!endif
!ifndef WEBVIEW2_BOOTSTRAPPER
  !define WEBVIEW2_BOOTSTRAPPER "MicrosoftEdgeWebView2Setup.exe"
!endif

Name "${APP_NAME}"
OutFile "${OUTFILE}"
InstallDir "$LOCALAPPDATA\Programs\MindFS"
InstallDirRegKey HKCU "${APP_REG_KEY}" "InstallDir"
RequestExecutionLevel user
SetCompressor /SOLID lzma
ShowInstDetails show
ShowUninstDetails show
BrandingText "${APP_NAME} ${VERSION}"
VIProductVersion "${VERSION4}"
VIAddVersionKey "ProductName" "${APP_NAME}"
VIAddVersionKey "ProductVersion" "${VERSION}"
VIAddVersionKey "FileDescription" "${APP_NAME} 安装程序"
VIAddVersionKey "FileVersion" "${VERSION}"
VIAddVersionKey "LegalCopyright" "MindFS"

Var NodeNameInput
Var NodeName
Var NodeNameHint
Var RegisterTries
Var StartTries
Var DeleteUserData
Var DeleteDataCheckbox
Var Wv2Found
Var Wv2Version
Var PathValue
Var PathNeedle
Var PathContains

; ---------------------------------------------------------------- 页面顺序
; 安装/卸载向导使用与应用同一套图标（构建脚本传 -DAPP_ICON=<app.ico>）。
; 没传图标时保持 NSIS 默认，仍能构建（本地手动 makensis 不会因为缺参数失败）。
!ifdef APP_ICON
  !define MUI_ICON "${APP_ICON}"
  !define MUI_UNICON "${APP_ICON}"
!endif
!define MUI_ABORTWARNING
!define MUI_WELCOMEPAGE_TITLE "安装 ${APP_NAME}"
!define MUI_WELCOMEPAGE_TEXT "${APP_NAME} 会把这台电脑接入你的主控，并在后台常驻。$\r$\n$\r$\n安装完成后可以立即打开桌面窗口，也可以随时从开始菜单启动。"

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
Page custom NodeNamePageShow NodeNamePageLeave
!define MUI_FINISHPAGE_RUN "$INSTDIR\bin\${APP_EXE}"
!define MUI_FINISHPAGE_RUN_TEXT "立即启动 ${APP_NAME}"
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
UninstPage custom un.DeleteDataPageShow un.DeleteDataPageLeave
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_UNPAGE_FINISH

!insertmacro MUI_LANGUAGE "SimpChinese"
!insertmacro MUI_LANGUAGE "English"

; ---------------------------------------------------------------- 主安装段
Section "${APP_NAME}" SecMain
  SectionIn RO

  Call EnsureWebView2
  Call StopRunningInstance

  ; 配置：control_sync.role=edge + update.auto_install=true。
  ; 走随包 exe 而不是在这里拼 JSON —— 用户已有配置必须保留未知键。
  DetailPrint "正在写入初始配置..."
  nsExec::ExecToStack '"$INSTDIR\bin\${APP_EXE}" --init-config'
  Pop $0
  Pop $1
  ${If} $0 != "0"
    DetailPrint "init-config: $1"
  ${EndIf}

  DetailPrint "正在复制文件..."
  SetOutPath "$INSTDIR"
  File /r "${PAYLOAD}\*.*"

  DetailPrint "正在创建开始菜单快捷方式..."
  CreateDirectory "$SMPROGRAMS"
  CreateShortCut "$SMPROGRAMS\${APP_NAME}.lnk" "$INSTDIR\bin\${APP_EXE}" "" "$INSTDIR\bin\${APP_EXE}" 0

  Call AddInstallDirToPath
  Call EnsureServiceTask

  WriteUninstaller "$INSTDIR\uninstall.exe"
  WriteRegStr HKCU "${APP_REG_KEY}" "InstallDir" "$INSTDIR"
  WriteRegStr HKCU "${APP_REG_KEY}" "Version" "${VERSION}"
  ${GetSize} "$INSTDIR" "/S=0K" $0 $1 $2
  WriteRegDWORD HKCU "${UNINST_KEY}" "EstimatedSize" $0
  WriteRegStr HKCU "${UNINST_KEY}" "DisplayName" "${APP_NAME}"
  WriteRegStr HKCU "${UNINST_KEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "${UNINST_KEY}" "Publisher" "${APP_NAME}"
  WriteRegStr HKCU "${UNINST_KEY}" "DisplayIcon" "$INSTDIR\bin\${APP_EXE}"
  WriteRegStr HKCU "${UNINST_KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${UNINST_KEY}" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegStr HKCU "${UNINST_KEY}" "QuietUninstallString" '"$INSTDIR\uninstall.exe" /S'
  WriteRegDWORD HKCU "${UNINST_KEY}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINST_KEY}" "NoRepair" 1

  ; 静默升级没有节点名页面，配置里已经有名字：直接起服务。
  ${If} ${Silent}
    Call StartServiceTask
  ${EndIf}
SectionEnd

; ---------------------------------------------------------------- 节点名页面
Function NodeNamePageShow
  ; 安装已完成，Back 会重跑安装段（重复拷贝、重复注册任务）：禁用。
  GetDlgItem $0 $HWNDPARENT 3
  EnableWindow $0 0

  ReadRegStr $NodeName HKCU "${APP_REG_KEY}" "NodeName"
  ${If} $NodeName == ""
    ReadEnvStr $NodeName "COMPUTERNAME"
  ${EndIf}

  !insertmacro MUI_HEADER_TEXT "节点名称" "这台设备在主控上显示的名字，重名会被拦下。"
  nsDialogs::Create 1018
  Pop $0
  ${If} $0 == error
    Abort
  ${EndIf}

  ${NSD_CreateLabel} 0 0 100% 24u "给这台设备起一个名字（默认是计算机名）："
  Pop $0
  ${NSD_CreateText} 0 26u 100% 13u "$NodeName"
  Pop $NodeNameInput
  ${NSD_CreateLabel} 0 44u 100% 34u "名字不能与主控上已有节点重复。还没配置主控时，重名检查会在配对时补做。"
  Pop $NodeNameHint

  nsDialogs::Show
FunctionEnd

Function NodeNamePageLeave
  ${NSD_GetText} $NodeNameInput $NodeName
  Call TrimNodeName

  Delete "$PLUGINSDIR\node-name.json"
  Delete "$PLUGINSDIR\node-name.json.txt"
  nsExec::ExecToStack '"$INSTDIR\bin\${APP_EXE}" --check-node-name "$NodeName" --out "$PLUGINSDIR\node-name.json"'
  Pop $0
  Pop $1

  ${If} $0 == "10"
    StrCpy $2 ""
    ${If} ${FileExists} "$PLUGINSDIR\node-name.json.txt"
      FileOpen $3 "$PLUGINSDIR\node-name.json.txt" r
      FileRead $3 $2
      FileClose $3
    ${EndIf}
    MessageBox MB_ICONEXCLAMATION|MB_OK "「$NodeName」已被主控上的另一个节点使用。$\r$\n$\r$\n$2$\r$\n请换一个名字。"
    Abort
  ${ElseIf} $0 == "11"
    StrCpy $2 ""
    ${If} ${FileExists} "$PLUGINSDIR\node-name.json.txt"
      FileOpen $3 "$PLUGINSDIR\node-name.json.txt" r
      FileRead $3 $2
      FileClose $3
    ${EndIf}
    MessageBox MB_ICONEXCLAMATION|MB_OK "节点名称不合法：$\r$\n$\r$\n$2"
    Abort
  ${ElseIf} $0 != "0"
    MessageBox MB_ICONQUESTION|MB_YESNO "无法连接主控检查重名。$\r$\n$\r$\n仍要使用「$NodeName」吗？重名会在配对时再次检查。" IDYES +2
    Abort
  ${EndIf}

  nsExec::ExecToStack '"$INSTDIR\bin\${APP_EXE}" --set-node-name "$NodeName"'
  Pop $0
  Pop $1
  ${If} $0 != "0"
    MessageBox MB_ICONSTOP "无法保存节点名称：$\r$\n$1"
    Abort
  ${EndIf}
  WriteRegStr HKCU "${APP_REG_KEY}" "NodeName" "$NodeName"

  Call StartServiceTask
FunctionEnd

; NSIS 没有内建 trim；名字前后带空格会在主控上变成两个不同的名字。
Function TrimNodeName
  ${Do}
    StrCpy $0 $NodeName 1
    ${If} $0 != " "
      ${ExitDo}
    ${EndIf}
    StrCpy $NodeName $NodeName "" 1
  ${Loop}
  ${Do}
    StrCpy $0 $NodeName 1 -1
    ${If} $0 != " "
      ${ExitDo}
    ${EndIf}
    StrCpy $NodeName $NodeName -1
  ${Loop}
FunctionEnd

; ---------------------------------------------------------------- WebView2
; 三处注册表都要看：per-user 安装（HKCU）、64 位系统上的 WOW6432Node、32 位系统。
; 缺运行时时先静默装内嵌的 Evergreen Bootstrapper；仍失败就交给用户选。
Function EnsureWebView2
  Call CheckWebView2
  ${If} $Wv2Found == "1"
    DetailPrint "WebView2 运行时：已安装（$Wv2Version）"
    Return
  ${EndIf}

  DetailPrint "未检测到 WebView2 运行时，正在安装..."
  InitPluginsDir
  File /oname=$PLUGINSDIR\MicrosoftEdgeWebView2Setup.exe "${WEBVIEW2_BOOTSTRAPPER}"

  wv2_retry:
  ExecWait '"$PLUGINSDIR\MicrosoftEdgeWebView2Setup.exe" /silent /install' $0
  Call CheckWebView2
  ${If} $Wv2Found == "1"
    DetailPrint "WebView2 运行时安装完成（$Wv2Version）"
    Return
  ${EndIf}

  ; 静默安装（自动更新）不能弹框：记日志后继续，桌面壳启动时会给出同样的指引。
  ${If} ${Silent}
    DetailPrint "WebView2 安装失败（静默模式），跳过。"
    Return
  ${EndIf}

  MessageBox MB_ICONEXCLAMATION|MB_YESNOCANCEL "无法自动安装 WebView2 运行时。$\r$\n$\r$\n是(Y)：重试$\r$\n否(N)：打开下载页$\r$\n取消：跳过（MindFS 窗口将打不开）" IDYES wv2_retry IDNO wv2_download
  Goto wv2_skip
  wv2_download:
    ExecShell "open" "https://developer.microsoft.com/microsoft-edge/webview2/"
  wv2_skip:
FunctionEnd

Function CheckWebView2
  StrCpy $Wv2Found "0"
  StrCpy $Wv2Version ""

  ReadRegStr $Wv2Version HKCU "Software\Microsoft\EdgeUpdate\Clients\${WEBVIEW2_GUID}" "pv"
  Call IsWebView2Version

  ReadRegStr $Wv2Version HKLM "SOFTWARE\Microsoft\EdgeUpdate\Clients\${WEBVIEW2_GUID}" "pv"
  Call IsWebView2Version

  SetRegView 64
  ReadRegStr $Wv2Version HKLM "SOFTWARE\Microsoft\EdgeUpdate\Clients\${WEBVIEW2_GUID}" "pv"
  Call IsWebView2Version
  SetRegView 32
FunctionEnd

Function IsWebView2Version
  ${If} $Wv2Version != ""
  ${AndIf} $Wv2Version != "0.0.0.0"
    StrCpy $Wv2Found "1"
  ${EndIf}
FunctionEnd

; ---------------------------------------------------------------- 进程与任务
Function StopRunningInstance
  nsExec::ExecToStack 'schtasks /query /tn "${TASK_NAME}"'
  Pop $0
  Pop $1
  ${If} $0 == "0"
    DetailPrint "正在停止后台服务..."
    nsExec::ExecToStack 'schtasks /end /tn "${TASK_NAME}"'
    Pop $0
    Pop $1
  ${EndIf}

  ; 桌面壳和任何残留实例都占着 bin\mindfs.exe，覆盖会「拒绝访问」。
  ; 这里不区分有没有计划任务：taskkill 是同步的（返回时进程已终止），
  ; 比 Sleep 猜时间可靠；受限令牌下可能失败，失败时 schtasks /end 已经生效。
  nsExec::ExecToStack 'taskkill /f /im ${APP_EXE}'
  Pop $0
  Pop $1
FunctionEnd

Function EnsureServiceTask
  nsExec::ExecToStack 'schtasks /query /tn "${TASK_NAME}"'
  Pop $0
  Pop $1
  ${If} $0 == "0"
    nsExec::ExecToStack 'schtasks /query /tn "${UPDATE_TASK_NAME}"'
    Pop $0
    Pop $1
    ${If} $0 == "0"
      DetailPrint "后台服务与更新任务已注册"
      Return
    ${EndIf}
    ; 旧版本只注册了服务任务。静默更新时没人能点 UAC，所以先让这次更新做完，
    ; 更新任务缺失只影响「下一次自动更新」，由用户下次手动安装补上。
    ${If} ${Silent}
      DetailPrint "缺少更新任务，静默模式下无法提权注册，本次跳过"
      Return
    ${EndIf}
  ${Else}
    ; 连服务任务都没有：装下去也没人拉起服务，静默模式必须失败而不是留下空安装。
    ${If} ${Silent}
      DetailPrint "缺少后台服务任务，静默模式下无法提权注册"
      SetErrorLevel 1
      Abort
    ${EndIf}
  ${EndIf}

  DetailPrint "正在注册后台服务（需要管理员权限）..."
  Delete "$PLUGINSDIR\register.json"
  Delete "$PLUGINSDIR\register.json.ok"
  ExecShell "runas" '"$INSTDIR\bin\${APP_EXE}"' '--register-service --result "$PLUGINSDIR\register.json"' SW_HIDE

  StrCpy $RegisterTries 0
  reg_wait:
    Sleep 500
    IntOp $RegisterTries $RegisterTries + 1
    ${If} ${FileExists} "$PLUGINSDIR\register.json.ok"
      DetailPrint "后台服务注册完成"
      Return
    ${EndIf}
    ${If} ${FileExists} "$PLUGINSDIR\register.json"
      FileOpen $2 "$PLUGINSDIR\register.json" r
      FileRead $2 $3
      FileClose $2
      ${If} ${Silent}
        DetailPrint "register-service failed: $3"
        SetErrorLevel 1
        Abort
      ${EndIf}
      MessageBox MB_ICONSTOP "注册后台服务失败：$\r$\n$\r$\n$3"
      Abort
    ${EndIf}
  ${If} $RegisterTries < 240
    Goto reg_wait
  ${EndIf}

  ${If} ${Silent}
    DetailPrint "register-service timed out"
    SetErrorLevel 1
    Abort
  ${EndIf}
  MessageBox MB_ICONSTOP "注册后台服务超时。请重新安装，并在系统询问时选择「是」授予管理员权限。"
  Abort
FunctionEnd

; 服务任务是 IgnoreNew：旧实例还没被计划任务判定为结束时，/run 会被静默吞掉。
; 所以不能「/run 一次就算启动」，要等进程真的出现；每次重试再 /run 一次，
; 重复的 /run 是幂等的（IgnoreNew 直接忽略）。
Function StartServiceTask
  StrCpy $StartTries 0
  start_retry:
    nsExec::ExecToStack 'schtasks /run /tn "${TASK_NAME}"'
    Pop $0
    Pop $1
    Sleep 1000
    ; tasklist 的输出没有本地化问题：/nh 去掉表头，命中行里一定有进程名。
    nsExec::ExecToStack 'tasklist /fi "imagename eq ${APP_EXE}" /nh /fo csv'
    Pop $0
    Pop $1
    StrCpy $0 $1
    StrCpy $1 "${APP_EXE}"
    Call StrContains
    ${If} $PathContains == "1"
      Return
    ${EndIf}
    IntOp $StartTries $StartTries + 1
    ${If} $StartTries < 15
      Goto start_retry
    ${EndIf}
    DetailPrint "启动后台服务失败：15 秒内没有看到 ${APP_EXE}"
FunctionEnd

; ---------------------------------------------------------------- PATH
Function AddInstallDirToPath
  ReadRegStr $PathValue HKCU "Environment" "Path"
  StrCpy $PathNeedle "$INSTDIR\bin"
  StrCpy $0 $PathValue
  StrCpy $1 $PathNeedle
  Call StrContains
  ${If} $PathContains == "1"
    Return
  ${EndIf}
  ${If} $PathValue == ""
    StrCpy $PathValue "$PathNeedle"
  ${Else}
    StrCpy $2 $PathValue 1 -1
    ${If} $2 != ";"
      StrCpy $PathValue "$PathValue;"
    ${EndIf}
    StrCpy $PathValue "$PathValue$PathNeedle"
  ${EndIf}
  WriteRegExpandStr HKCU "Environment" "Path" "$PathValue"
  System::Call 'user32::SendMessageTimeoutW(i 0xffff, i 0x001A, i 0, w "Environment", i 2, i 5000, *i .r0)'
FunctionEnd

; 不区分大小写的子串查找（StrCmp 默认忽略大小写）。
; 入参 $0=haystack $1=needle，出参 $PathContains=1/0。
Function StrContains
  StrCpy $PathContains "0"
  StrLen $2 $1
  ${If} $2 == 0
    Return
  ${EndIf}
  StrCpy $3 0
  sc_loop:
    StrCpy $4 $0 $2 $3
    ${If} $4 == ""
      Return
    ${EndIf}
    StrCmp $4 $1 sc_found
    IntOp $3 $3 + 1
    Goto sc_loop
  sc_found:
    StrCpy $PathContains "1"
FunctionEnd

; ---------------------------------------------------------------- 卸载
Function un.DeleteDataPageShow
  !insertmacro MUI_HEADER_TEXT "卸载 ${APP_NAME}" "程序与后台服务会被删除，本机数据默认保留。"
  nsDialogs::Create 1018
  Pop $0
  ${If} $0 == error
    Abort
  ${EndIf}
  ${NSD_CreateLabel} 0 0 100% 34u "卸载会删除程序文件、开始菜单快捷方式、PATH 条目和后台服务。"
  Pop $0
  ${NSD_CreateCheckbox} 0 40u 100% 12u "同时删除本机数据（配置、会话与日志，不可恢复）"
  Pop $DeleteDataCheckbox
  nsDialogs::Show
FunctionEnd

Function un.DeleteDataPageLeave
  ${NSD_GetState} $DeleteDataCheckbox $DeleteUserData
FunctionEnd

Section "Uninstall"
  nsExec::ExecToStack 'schtasks /end /tn "${TASK_NAME}"'
  Pop $0
  Pop $1

  InitPluginsDir
  Delete "$PLUGINSDIR\unregister.json"
  Delete "$PLUGINSDIR\unregister.json.ok"
  ${If} ${FileExists} "$INSTDIR\bin\${APP_EXE}"
    ExecShell "runas" '"$INSTDIR\bin\${APP_EXE}"' '--unregister-service --result "$PLUGINSDIR\unregister.json"' SW_HIDE
    StrCpy $RegisterTries 0
    unreg_wait:
      Sleep 500
      IntOp $RegisterTries $RegisterTries + 1
      ${If} ${FileExists} "$PLUGINSDIR\unregister.json.ok"
        Goto unreg_done
      ${EndIf}
      ${If} ${FileExists} "$PLUGINSDIR\unregister.json"
        Goto unreg_done
      ${EndIf}
    ${If} $RegisterTries < 120
      Goto unreg_wait
    ${EndIf}
    unreg_done:
    ; 注销失败不阻止卸载：文件仍要删掉，残留任务由用户手动清（会打印在详情里）。
    Delete "$PLUGINSDIR\unregister.json"
    Delete "$PLUGINSDIR\unregister.json.ok"
  ${EndIf}

  nsExec::ExecToStack 'taskkill /f /im ${APP_EXE}'
  Pop $0
  Pop $1

  Delete "$SMPROGRAMS\${APP_NAME}.lnk"
  Call un.RemoveFromUserPath

  DeleteRegKey HKCU "${UNINST_KEY}"
  DeleteRegKey HKCU "${APP_REG_KEY}"
  RMDir /r "$INSTDIR"

  ${If} $DeleteUserData == ${BST_CHECKED}
    RMDir /r "$APPDATA\mindfs"
    RMDir /r "$LOCALAPPDATA\mindfs"
  ${EndIf}
SectionEnd

Function un.RemoveFromUserPath
  ReadRegStr $PathValue HKCU "Environment" "Path"
  ${If} $PathValue == ""
    Return
  ${EndIf}
  StrCpy $1 ""
  StrCpy $2 $PathValue
  rp_next:
    ${If} $2 == ""
      Goto rp_flush
    ${EndIf}
    StrCpy $3 0
  rp_scan:
    StrCpy $4 $2 1 $3
    ${If} $4 == ""
      StrCpy $4 $2
      StrCpy $2 ""
      Goto rp_compare
    ${EndIf}
    ${If} $4 == ";"
      StrCpy $4 $2 $3
      IntOp $3 $3 + 1
      StrCpy $2 $2 "" $3
      Goto rp_compare
    ${EndIf}
    IntOp $3 $3 + 1
    Goto rp_scan
  rp_compare:
    StrCmp $4 "$INSTDIR\bin" rp_next
    ${If} $1 == ""
      StrCpy $1 "$4"
    ${Else}
      StrCpy $1 "$1;$4"
    ${EndIf}
    Goto rp_next
  rp_flush:
    WriteRegExpandStr HKCU "Environment" "Path" "$1"
FunctionEnd
