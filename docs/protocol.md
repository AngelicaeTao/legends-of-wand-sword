# 通信协议（Cocos 客户端 ↔ Nakama 服务端）

统一走 Nakama WebSocket/WSS 实时连接，JSON 消息带 `type` / `seq` / `v` 三字段。
请求响应成对，错误带 `code` + `msg`。协议版本 `v = 1`，向前兼容。

> 客户端强类型定义见 `client/assets/scripts/core/Protocol.ts`；
> 服务端权威结算入参见 `server/internal/game/battle.go` 的 `BattleRequest`。

## 信封格式

```json
{ "type": "engage", "seq": 7, "v": 1, "data": { ... } }
```

| 字段 | 说明 |
|------|------|
| `type` | 消息类型 |
| `seq` | 客户端递增序号，用于匹配回包 |
| `v` | 协议版本，不匹配丢弃 |
| `data` | 业务载荷 |
| `code` / `msg` | 错误时返回 |

## 核心消息

| 方向 | type | 主要字段 | 说明 |
|------|------|----------|------|
| C→S | `login` | deviceId / username+password | 设备登录（内测零输入）或账密 |
| C→S | `submit_bd` | slots[4] | 战前技能配置，战斗中不生效 |
| C→S | `engage` | monsterId、monsterLv、elite、seed | 请求交战，服务端起权威战斗 |
| S→C | `battle_report` | rounds[]、result、rewards、seed | 战报，客户端只表现 |
| S→C | `state_snapshot` | level/gold/stamina/lastSeen/坐标 | 登录/重连后权威快照 |
| C→S | `claim_idle` | — | 请求结算离线收益 |
| C→S | `match_req` / `join` | mode、roomId | 实时多人匹配/加入 |
| S→C | `player_move` | playerId、x、y、ts、action | 位置广播，客户端插值 |
| 双向 | `heartbeat` / `pong` | ts | 心跳保活与断线判定 |

## standalone 模式的 HTTP API（初跑验证用）

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/health` | 健康检查（含 `persist` 字段） |
| GET | `/api/config` | 全量配置 |
| GET | `/api/tables` | 分表清单 |
| POST | `/api/stats/calc` | 属性计算（body: Avatar） |
| POST | `/api/character/new` | 创角并落盘（body: `{userId,name,classId}`） |
| POST | `/api/state/load` | 读档（body: `{userId}`） |
| POST | `/api/battle/resolve` | **纯**战斗结算，不落库（body: BattleRequest，回放用） |
| POST | `/api/battle/fight` | 战斗结算 + 奖励入账 + 落库（body: `{userId,bd,monsterId,monsterLv,elite}`） |
| POST | `/api/idle/settle` | **纯**离线结算，不落库（body: IdleInput） |
| POST | `/api/idle/finalize` | 离线结算 + 入账 + 推进 lastSeen + 落库（body: `{userId}`） |
| POST | `/api/transfer` | 转职 + 装备按新职业系数换算 + 落库（body: `{userId,to}`） |

> `battle/resolve` 与 `idle/settle` 是纯函数（便于复现/验证）；`fight`/`finalize`/`transfer` 才写存档。
> 存档落盘于 `data/saves/<userId>.json`（原子写，防损坏）；生产模式对应 Nakama Storage。

## 权威原则

- 战斗结果、离线收益、随机种子**全部服务端生成**，客户端下发的任何数值不采信。
- `engage` 的 `seed` 仅用于复现（>0 时采用，=0 服务端随机），防客户端刷结果靠"结果不可伪造"而非种子本身。
- 断线重连后以 `state_snapshot` 覆盖本地缓存。
- 战斗中提交 BD 不生效（服务端读战前快照）。

## Nakama 端点

- HTTP/WebSocket：`ws://127.0.0.1:7350/ws?key=<serverKey>`
- 公网内测：`wss://<随机>.trycloudflare.com/ws?key=<serverKey>`（Cloudflare 自动签发 WSS 证书）
- 控制台：`http://127.0.0.1:7351`（仅本机，勿暴露公网）
