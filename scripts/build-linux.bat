@echo off
rem Build Matea for Linux on Windows (cmd.exe).
rem Usage:
rem   scripts\build-linux.bat        rem linux/amd64
rem   scripts\build-linux.bat amd64   rem explicit arch
rem   scripts\build-linux.bat arm64   rem ARM64 servers

setlocal enabledelayedexpansion
set "ARCH=%1"
if "%ARCH%"=="" set "ARCH=amd64"
if "%ARCH%"=="x86_64" set "ARCH=amd64"
if "%ARCH%"=="aarch64" set "ARCH=arm64"

set "OUTPUT=dist\matea-linux-%ARCH%"
if not exist dist mkdir dist

rem Build the embedded web UI first so go:embed bundles the latest assets.
where npm > nul 2> nul
if errorlevel 1 (
  echo Error: npm is required to build the web UI, but it was not found.
  exit /b 1
)

pushd web
if not exist node_modules (
  echo Installing web dependencies...
  call npm install
  if errorlevel 1 (
    echo Failed to install web dependencies.
    popd
    exit /b 1
  )
)

echo Building web UI...
call npm run build
if errorlevel 1 (
  echo Web UI build failed.
  popd
  exit /b 1
)
popd

echo Building Matea for linux/%ARCH% ...
set CGO_ENABLED=0
set GOOS=linux
set GOARCH=%ARCH%

go build -ldflags="-s -w" -o "%OUTPUT%" .
if errorlevel 1 (
  echo Build failed.
  exit /b 1
)

echo Built: %OUTPUT%
for %%F in ("%OUTPUT%") do echo Size: %%~zF bytes
pause
