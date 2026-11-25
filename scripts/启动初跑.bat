@echo off
chcp 936 >nul
cd /d "%~dp0.."
echo ==========================================
echo   杖剑传说 - A 方案初跑版
echo ==========================================

where go >nul 2>nul
if errorlevel 1 (
    echo [错误] 未检测到 Go，请先安装 1.21+ 并加入 PATH
    pause
    exit /b 1
)

echo [1/3] 编译权威服务...
cd server
set GO111MODULE=on
go build -o ..\data\zjcs-server.exe .
if errorlevel 1 (
    echo [错误] 编译失败
    pause
    exit /b 1
)
cd ..
echo       编译完成

echo [2/3] 启动权威服务 端口 8080...
start "zjcs-server" /B data\zjcs-server.exe -addr :8080 -config configs

echo [3/3] 完成
echo.
echo   验证页：用浏览器打开 web\index.html
echo   健康检查：curl http://127.0.0.1:8080/health
echo.
echo   如需 Nakama：在 server 目录执行 docker compose up -d
echo.
pause
