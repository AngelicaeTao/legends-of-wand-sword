#!/bin/bash
# 杖剑传说 · ngrok 常驻隧道启动脚本（v1.9.22）
# 固定地址：https://bonnet-convene-refract.ngrok-free.dev （永久不变）
# 用法：./scripts/ngrok-tunnel.sh   （游戏服务需先在 localhost:3000 运行）
ngrok http 3000 --domain=bonnet-convene-refract.ngrok-free.dev --log=stdout --log-format=json
