@echo off
setlocal

rem ===== Editable CLI packaging configuration =====
set "JAVA_SOURCE_ROOT=D:\workspace\alpha\backend\alpha-java-cloud"
set "WEB_DIST_ROOT=D:\workspace\alpha\fronted\alpha-web-vue3\dist"
set "OUTPUT_PATH=%CD%\aifar-batch-update.zip"
rem ================================================

set "SERVICES=%*"
if not defined SERVICES set "SERVICES=all"

set "SCRIPT_DIR=%~dp0"
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%SCRIPT_DIR%package-aifar-artifact-bundle.ps1" -JavaSourceRoot "%JAVA_SOURCE_ROOT%" -WebDistRoot "%WEB_DIST_ROOT%" -OutputPath "%OUTPUT_PATH%" -Services "%SERVICES%"
exit /b %ERRORLEVEL%
