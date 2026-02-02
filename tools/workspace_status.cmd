@echo off
REM Workspace status script for Bazel stamping (Windows version)
REM This script outputs key-value pairs that can be used for build stamping

setlocal enabledelayedexpansion

REM Get version from environment variable or fall back to git describe
if defined GALA_VERSION (
    echo STABLE_GALA_VERSION %GALA_VERSION%
) else (
    for /f "tokens=*" %%i in ('git describe --tags --always 2^>nul') do set VERSION=%%i
    if not defined VERSION set VERSION=dev
    echo STABLE_GALA_VERSION !VERSION!
)

REM Git commit SHA
for /f "tokens=*" %%i in ('git rev-parse HEAD 2^>nul') do set COMMIT=%%i
if not defined COMMIT set COMMIT=unknown
echo STABLE_GIT_COMMIT %COMMIT%

REM Build timestamp (UTC)
for /f "tokens=*" %%i in ('powershell -Command "[DateTime]::UtcNow.ToString('yyyy-MM-ddTHH:mm:ssZ')"') do set BUILD_DATE=%%i
if not defined BUILD_DATE set BUILD_DATE=unknown
echo STABLE_BUILD_DATE %BUILD_DATE%
