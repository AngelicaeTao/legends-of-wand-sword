#!/usr/bin/env bash
# 杖剑传说 · 本地一键启动 / 停止脚本
# 用法：
#   ./run.sh start    编译并启动（默认 http://localhost:8080）
#   ./run.sh stop     停止
#   ./run.sh status   查看状态与健康检查
#   ./run.sh restart  重启
#   PORT=9000 ./run.sh start    换端口
set -euo pipefail
cd "$(dirname "$0")"

PORT="${PORT:-8080}"
BIN="data/zjcs-server"
LOG="data/server.log"
PIDF="data/server.pid"

build() {
  command -v go >/dev/null || { echo "缺少 Go 1.21+"; exit 1; }
  echo "[build] 编译服务端…"
  ( cd server && GO111MODULE=on go build -o "../$BIN" . )
  echo "[build] 完成 → $BIN"
}

# 是否需要重编译：二进制缺失，或 server/ 下有 .go 比二进制新。
# 缺这一段时，切分支 / git pull 后直接 start 会继续跑旧二进制，
# 服务起来看似正常，但新增路由全部 404（落到静态兜底返回 index.html）。
needs_build() {
  [ -x "$BIN" ] || return 0
  [ -n "$(find server -name '*.go' -newer "$BIN" -print -quit 2>/dev/null)" ]
}

start() {
  if needs_build; then build; fi
  if pgrep -x zjcs-server >/dev/null 2>&1; then
    echo "服务已在运行（pid $(pgrep -x zjcs-server | tr '\n' ' ')），如需重启：$0 restart"
    exit 0
  fi
  mkdir -p data/saves
  # v1.9.91 服务端只允许绑回环地址（上游 a937821 安全加固），这里显式给 127.0.0.1
  echo "[start] 监听 127.0.0.1:$PORT  配置 configs  前端 web/index.html  资源 web/res"
  setsid "./$BIN" -addr "127.0.0.1:$PORT" -config configs -web web/index.html -res web/res > "$LOG" 2>&1 &
  echo $! > "$PIDF"
  for _ in $(seq 1 20); do
    sleep 0.5
    if curl -fsS -m 2 "http://127.0.0.1:$PORT/health" >/dev/null 2>&1; then
      echo "[start] 就绪 → http://localhost:$PORT"
      curl -s "http://127.0.0.1:$PORT/health"; echo
      return 0
    fi
  done
  echo "[start] 启动失败，日志尾部："; tail -20 "$LOG"; exit 1
}

stop() {
  local pids
  pids="$(pgrep -x zjcs-server || true)"
  if [ -z "$pids" ]; then echo "[stop] 未在运行"; return 0; fi
  # SIGTERM：触发优雅关闭，退出前刷新异步落盘存档
  for p in $pids; do kill "$p" 2>/dev/null || true; done
  for _ in $(seq 1 20); do
    sleep 0.3
    pgrep -x zjcs-server >/dev/null 2>&1 || { echo "[stop] 已停止（存档已刷盘）"; rm -f "$PIDF"; return 0; }
  done
  echo "[stop] 优雅关闭超时，强制结束"; pkill -9 -x zjcs-server || true; rm -f "$PIDF"
}

status() {
  if pgrep -x zjcs-server >/dev/null 2>&1; then
    echo "运行中 pid: $(pgrep -x zjcs-server | tr '\n' ' ')"
    curl -s "http://127.0.0.1:$PORT/health"; echo
    echo "存档数: $(ls data/saves 2>/dev/null | wc -l)"
  else
    echo "未运行"
  fi
}

case "${1:-start}" in
  start) start ;;
  stop) stop ;;
  restart) stop; start ;;
  status) status ;;
  build) build ;;
  *) echo "用法: $0 {start|stop|restart|status|build}"; exit 1 ;;
esac
