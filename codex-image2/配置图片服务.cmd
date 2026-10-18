@echo off
setlocal
set "IMAGE2_ARCH=amd64"
if /I "%PROCESSOR_ARCHITECTURE%"=="ARM64" set "IMAGE2_ARCH=arm64"
if /I "%PROCESSOR_ARCHITEW6432%"=="ARM64" set "IMAGE2_ARCH=arm64"
"%~dp0bin\codex-image2-windows-%IMAGE2_ARCH%.exe" configure
if errorlevel 1 pause
