@echo off
setlocal

if "%~1"=="-h"     goto :usage
if "%~1"=="--help" goto :usage
if "%~1"=="/?"     goto :usage

set "PORT=%~1"
if "%PORT%"=="" set "PORT=8080"
set "APP_REPLICAS=%~2"
if "%APP_REPLICAS%"=="" set "APP_REPLICAS=3"

set /a "PORT_INT=PORT" 2>nul
if errorlevel 1 goto :bad_port
if not "%PORT_INT%"=="%PORT%" goto :bad_port
if %PORT_INT% LSS 1 goto :bad_port
if %PORT_INT% GTR 65535 goto :bad_port

set /a "REPLICAS_INT=APP_REPLICAS" 2>nul
if errorlevel 1 goto :bad_replicas
if not "%REPLICAS_INT%"=="%APP_REPLICAS%" goto :bad_replicas
if %REPLICAS_INT% LSS 1 goto :bad_replicas

cd /d "%~dp0.."

docker compose -f deploy\docker-compose.yml up --build
exit /b %errorlevel%

:bad_port
1>&2 echo error: PORT must be an integer in 1-65535, got: %PORT%
exit /b 1

:bad_replicas
1>&2 echo error: APP_REPLICAS must be a positive integer, got: %APP_REPLICAS%
exit /b 1

:usage
echo Usage: %~nx0 [PORT] [APP_REPLICAS]
echo.
echo   PORT          Host port published by Caddy (default: 8080)
echo   APP_REPLICAS  Number of app instances behind the load balancer (default: 3)
echo.
echo Examples:
echo   %~nx0             # 3 replicas on :8080
echo   %~nx0 9090        # 3 replicas on :9090
echo   %~nx0 9090 5      # 5 replicas on :9090
exit /b 0
