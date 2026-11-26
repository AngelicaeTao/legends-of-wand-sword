# 大世界多人同步（Match handler）

## 作用

让同一世界（worldId）的玩家进入同一条线，能在彼此屏幕上看到移动。这是"大世界探索"从单机变联网的基础。

> 边界：只做**位置同步 + 同屏显示**。观战 / 世界 BOSS / 公会 / 交易行属需求文档 Out of Scope，未实现。

## 架构

```
客户端 A ──move(op=1)──▶ Nakama Match(world) ──snapshot(op=2,10Hz)──▶ 客户端 A/B/C
                              │
                              服务端权威坐标 + 限速校验 + 人数上限
```

- 客户端只上报**意图**（`move`），自己的坐标也不采信；
- 服务端按 worldId 分线，保存每个 session 的权威坐标；
- 每 100ms（tickRate=10）广播一次全场快照，客户端只读渲染；
- 防作弊：`speedOK` 判定位移速度 > 600 单位/秒即丢弃（瞬移无效）；单线人数上限 40。

## 协议（Nakama realtime opCode）

| opCode | 方向 | 消息 | 说明 |
|--------|------|------|------|
| 1 | C→S | `move` | `{x,y,ts,action}`，action ∈ idle/walk/battle |
| 2 | S→C | `snapshot` | `{players:[{playerId,x,y,action,ts}],tick,worldId}`，10Hz |
| 3 | S→C | `join` | `{playerId}` 新玩家加入 |
| 4 | S→C | `leave` | `{playerId}` 玩家离开 |

另有 RPC：

| RPC | 请求 | 返回 | 说明 |
|-----|------|------|------|
| `join_world` | `{worldId}` | `{matchId,worldId}` | 找/建该世界的线，客户端再 socket.matchJoin |

## 关键文件

| 文件 | 职责 |
|------|------|
| `server/nakama_module/match.go` | Match 生命周期 + 限速 + 快照广播（含 `speedOK` 纯函数） |
| `server/nakama_module/match_test.go` | 防瞬移 / 快照序列化单测 |
| `server/nakama_module/main.go` | 注册 Match("world") + RPC("join_world") |
| `client/assets/scripts/cocos/RemotePlayerLayer.ts` | 客户端承接 `player_move`，渲染远端玩家 |

## 验证

用官方镜像跑单测（含防瞬移 6 用例、快照回环）：

```bash
cd server
docker run --rm -v "$(pwd)":/app -w /app/nakama_module \
  -e GOPROXY='https://goproxy.cn,direct' \
  heroiclabs/nakama-pluginbuilder:3.24.0 test -tags nakama -v ./...
```

构建插件（含 Match）：`bash scripts/build-plugin.sh`

> **本沙箱说明**：插件能编译出 `.so`，但 Nakama 官方二进制用非公开 build ID，加载时仍报 `internal/goarch` 不匹配——与本轮之前的 RPC 插件同一环境限制。代码与单测完整，部署机按 `build-plugin.sh` 用该机 Nakama 二进制同环境重建即可加载。核心位置同步逻辑已通过单测验证。
