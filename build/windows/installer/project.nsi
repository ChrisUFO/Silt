Unicode true

####
## Custom Silt NSIS installer template (#install-scope).
## Per-user-only installer. Silt is a small single-user app, so every install
## lands in the current user's profile and never needs elevation — this drops
## the prior MultiUser scope-choice machinery (and the UAC prompt its "Highest"
## manifest forced for admin users). Each Windows user who wants Silt simply
## installs their own copy; no admin, no consent dialog.
##
## This file is automatically used by `wails build --nsis` because it lives at
## build/windows/installer/project.nsi, which takes precedence over the Wails
## embedded template.
####

## Per-user execution level: "user" yields an asInvoker manifest, so the
## installer runs with the launching user's token and never triggers UAC — for
## admins or standard users alike.
!define REQUEST_EXECUTION_LEVEL "user"

## Include the wails tools (fills in INFO_*, PRODUCT_EXECUTABLE, macros, etc.)
!include "wails_tools.nsh"

# Version info
VIProductVersion "${INFO_PRODUCTVERSION}.0"
VIFileVersion    "${INFO_PRODUCTVERSION}.0"
VIAddVersionKey "CompanyName"     "${INFO_COMPANYNAME}"
VIAddVersionKey "FileDescription" "${INFO_PRODUCTNAME} Installer"
VIAddVersionKey "ProductVersion"  "${INFO_PRODUCTVERSION}"
VIAddVersionKey "FileVersion"     "${INFO_PRODUCTVERSION}"
VIAddVersionKey "LegalCopyright"  "${INFO_COPYRIGHT}"
VIAddVersionKey "ProductName"     "${INFO_PRODUCTNAME}"

ManifestDPIAware true

!include "MUI.nsh"

!define MUI_ICON "..\icon.ico"
!define MUI_UNICON "..\icon.ico"
!define MUI_FINISHPAGE_NOAUTOCLOSE
!define MUI_ABORTWARNING

## Pages: Welcome → Directory → Install → Finish. No scope-choice page: there
## is only one scope now (current user).
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "English"

Name "${INFO_PRODUCTNAME}"
OutFile "..\..\bin\${INFO_PROJECTNAME}-${ARCH}-installer.exe"
## Fixed per-user install location. This matches the prior MultiUser
## CurrentUser default, so existing per-user installs upgrade in place.
InstallDir "$LOCALAPPDATA\Programs\${INFO_COMPANYNAME}\${INFO_PRODUCTNAME}"
ShowInstDetails show

Function .onInit
    ## Architecture guard (from wails_tools.nsh).
    !insertmacro wails.checkArchitecture

    ## Preserve a prior install directory across upgrades. The current per-user
    ## format stores InstallDir under the uninstall key; the older MultiUser
    ## template stored it under Software\<Company>\<Product>. Check both so an
    ## upgrade from either format lands in the same place; otherwise fall back
    ## to the InstallDir default above.
    SetRegView 64
    ReadRegStr $0 HKCU "${UNINST_KEY}" "InstallDir"
    ${If} $0 == ""
        ReadRegStr $0 HKCU "Software\${INFO_COMPANYNAME}\${INFO_PRODUCTNAME}" "InstallDir"
    ${EndIf}
    ${If} $0 != ""
        StrCpy $INSTDIR $0
    ${EndIf}
FunctionEnd

Section
    ## Per-user: shortcuts + registry belong to the current user only.
    SetShellVarContext current

    !insertmacro wails.webview2runtime

    ## If a prior version is installed, silently run its uninstaller first for
    ## a clean upgrade (no leftover stale files). The uninstall string lives in
    ## HKCU now that every install is per-user.
    SetRegView 64
    ReadRegStr $0 HKCU "${UNINST_KEY}" "UninstallString"
    ${If} $0 != ""
        ExecWait '"$0" /S _?=$INSTDIR'
    ${EndIf}

    SetOutPath $INSTDIR

    !insertmacro wails.files

    CreateShortcut "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
    CreateShortCut "$DESKTOP\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"

    !insertmacro wails.associateFiles
    !insertmacro wails.associateCustomProtocols

    ## Write uninstaller + per-user registry entries (Add/Remove Programs).
    ## InstallDir is recorded here so the next upgrade can reuse it.
    WriteUninstaller "$INSTDIR\uninstall.exe"
    SetRegView 64
    WriteRegStr HKCU "${UNINST_KEY}" "Publisher" "${INFO_COMPANYNAME}"
    WriteRegStr HKCU "${UNINST_KEY}" "DisplayName" "${INFO_PRODUCTNAME}"
    WriteRegStr HKCU "${UNINST_KEY}" "DisplayVersion" "${INFO_PRODUCTVERSION}"
    WriteRegStr HKCU "${UNINST_KEY}" "DisplayIcon" "$INSTDIR\${PRODUCT_EXECUTABLE}"
    WriteRegStr HKCU "${UNINST_KEY}" "UninstallString" "$\"$INSTDIR\uninstall.exe$\""
    WriteRegStr HKCU "${UNINST_KEY}" "QuietUninstallString" "$\"$INSTDIR\uninstall.exe$\" /S"
    WriteRegStr HKCU "${UNINST_KEY}" "InstallDir" "$INSTDIR"
    ${GetSize} "$INSTDIR" "/S=0K" $0 $1 $2
    IntFmt $0 "0x%08X" $0
    WriteRegDWORD HKCU "${UNINST_KEY}" "EstimatedSize" "$0"
SectionEnd

Section "uninstall"
    SetShellVarContext current

    RMDir /r "$AppData\${PRODUCT_EXECUTABLE}"
    RMDir /r $INSTDIR

    Delete "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk"
    Delete "$DESKTOP\${INFO_PRODUCTNAME}.lnk"

    !insertmacro wails.unassociateFiles
    !insertmacro wails.unassociateCustomProtocols

    Delete "$INSTDIR\uninstall.exe"
    SetRegView 64
    DeleteRegKey HKCU "${UNINST_KEY}"
SectionEnd
