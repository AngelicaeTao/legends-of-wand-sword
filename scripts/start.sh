#!/usr/bin/env bash
# 杖剑传说初跑版 · 一键启动（Linux / Mac）
# 用法：bash scripts/start.sh
set -e
cd "$(dirname "$0")/.."
echo "=========================================="
echo "  杖剑传说 · A 方案初跑版"
echo "=========================================="

# 1) 检查依赖
command -v go >/dev/null || { echo "缺少 Go，请先安装 1.21+"; exit 1; }
command -v docker >/dev/null || { echo "缺少 Docker（仅 Nakama 模式需要）"; }

# 2) 编译权威服务
echo "[1/3] 编译权威服务..."
cd server
GO111MODULE=on go build -o ../data/zjcs-server .
cd ..
echo "      ✓ 编译完成"

# 3) 启动权威服务（standalone，立即可用）
echo "[2/3] 启动权威服务（HTTP :8080）..."
pkill -f 'zjcs-server' 2>/dev/null || true
./data/zjcs-server -addr :8080 -config configs > data/server.log 2>&1 &
echo "      ✓ 已启动，日志 data/server.log"

# 4) 提示
echo "[3/3] 完成"
echo ""
echo "  验证页：用浏览器打开  web/index.html"
echo "  健康检查：curl http://127.0.0.1:8080/health"
echo ""
echo "  如需 Nakama（账号/匹配/联网）：在 server/ 执行 docker compose up -d"
echo "    客户端 API :7350  控制台 :7351（勿暴露公网）"
echo ""
echo "  停止服务：pkill -f zjcs-server"
