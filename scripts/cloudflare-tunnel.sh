#!/usr/bin/env bash
# Cloudflare 命名隧道（固定域名）：https://game.nantong519.dpdns.org
# 隧道名 zjcs-legend（账号 29eb61d5...，ingress 在 Cloudflare 远端配置：game.* → localhost:3000）
# 用法：先启动游戏 ./data/zjcs-server -addr :3000 ...，再执行本脚本
cloudflared --no-autoupdate tunnel run --token "$(cat /root/.cloudflared/zjcs-legend.token)"
