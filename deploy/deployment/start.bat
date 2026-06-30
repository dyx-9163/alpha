@echo off
setlocal
set "ROOT=%~dp0"
if exist "%ROOT%config\defaults.env" (
  for /f "usebackq eol=# tokens=1,* delims==" %%A in ("%ROOT%config\defaults.env") do (
    if not "%%A"=="" if not defined %%A set "%%A=%%B"
  )
)
if "%AIFAR_DEFAULT_PASSWORD%"=="" set "AIFAR_DEFAULT_PASSWORD=Oversea.123"
if "%AIFAR_BOOTSTRAP_PASSWORD%"=="" set "AIFAR_BOOTSTRAP_PASSWORD=%AIFAR_DEFAULT_PASSWORD%"
if "%AIFAR_DEFAULT_DEPLOY_DIR%"=="" set "AIFAR_DEFAULT_DEPLOY_DIR=/aifar/apps"
if "%AIFAR_STATIC_DIR%"=="" set "AIFAR_STATIC_DIR=%ROOT%web\dist"
if "%AIFAR_RESOURCE_DIR%"=="" set "AIFAR_RESOURCE_DIR=%ROOT%resources"
if "%AIFAR_ADDR%"=="" set "AIFAR_ADDR=0.0.0.0:8080"
if not exist "%ROOT%bin\aifar-server-windows-amd64.exe" (
  echo Missing backend binary: "%ROOT%bin\aifar-server-windows-amd64.exe"
  echo Build it with scripts\package.ps1 or extract bin from aifar-deployment.zip.
  exit /b 1
)
cd /d "%ROOT%"
"%ROOT%bin\aifar-server-windows-amd64.exe"
