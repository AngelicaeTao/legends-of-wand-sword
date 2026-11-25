#!/usr/bin/env bash
# 构建 Nakama Go 权威 RPC 插件（plugins/zjcs.so）—— 强烈推荐用官方镜像。
#
# ⚠️ Go plugin 必须与 Nakama 二进制在「Go 版本 + 标准库编译环境」两方面完全一致，
#    否则 Nakama 启动即崩（no plugin module data / different version of package internal/goarch）。
#    唯一可靠方式是用官方 nakama-pluginbuilder:3.24.0 镜像（内含与 Nakama 一致的工具链）。
#
# 插件是独立 module（nakama_module/，go 1.23.2），主 module 保持 go 1.21 供 standalone 使用。
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT/server"

if ! docker image inspect heroiclabs/nakama-pluginbuilder:3.24.0 >/dev/null 2>&1; then
  echo "请先拉取构建镜像：docker pull heroiclabs/nakama-pluginbuilder:3.24.0"
  exit 1
fi

mkdir -p plugins
echo "=== 1/2 整理插件 module 依赖 ==="
docker run --rm -v "$(pwd)":/app -w /app/nakama_module \
  -e GOPROXY='https://goproxy.cn,direct' \
  heroiclabs/nakama-pluginbuilder:3.24.0 mod tidy

echo "=== 2/2 构建插件 ==="
docker run --rm -v "$(pwd)":/app -w /app/nakama_module \
  -e GOPROXY='https://goproxy.cn,direct' \
  heroiclabs/nakama-pluginbuilder:3.24.0 \
  build -tags nakama -buildmode=plugin -o /app/plugins/zjcs.so .

echo "✅ 已生成 plugins/zjcs.so（内嵌 $(grep -aoE 'go1\.[0-9]+\.[0-9]+' plugins/zjcs.so | sort -u | head -1)）"
ls -lh plugins/zjcs.so
