#!/usr/bin/env bash
# Cloudflare 快速隧道联机（无需账号；地址每次启动随机，正式使用请改命名隧道绑域名）
# 用法：先启动游戏 ./data/zjcs-server -addr :3000 ...，再执行本脚本
command -v cloudflared >/dev/null || { echo "未安装 cloudflared"; exit 1; }
cloudflared tunnel --url http://localhost:3000 --no-autoupdate
