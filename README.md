# 杖剑传说类放置手游 · A 方案初跑版

> 架构：Cocos Creator + TypeScript 客户端 / Go + Nakama 服务端 / WebSocket(WSS) 通信 / 分表存档。
> 本包是**可灵活修改的最小工程骨架**，配置驱动、服务端权威，数值全部外置。
> 对应需求文档 v0.6（A 方案联网版）、地图设计 v0.4、像素资源包 v2.0。

---

## 快速开始（30 秒）

需要：Go 1.21+（Nakama 模式另需 Docker）。

```bash
# Linux / Mac
bash scripts/start.sh
# Windows：双击 scripts/启动初跑.bat
```

然后用浏览器打开 `web/index.html` → 顶部显示"服务端已连接"即可验证：
- 改职业/等级 → 点「计算属性」（服务端算）
- 选技能 BD、选怪物 → 点「交战」（服务端权威结算，逐回合演出战报）
- 点「结算离线收益」（服务端时间戳，封顶 8h）

健康检查：`curl http://127.0.0.1:8080/health`

---

## 目录结构

```
杖剑传说-初跑版/
├── configs/               ★ 全部数值/内容配置（改这里即可调游戏）
│   ├── classes.json         职业/转职树/等级成长
│   ├── skills.json          技能
│   ├── equipment.json       装备/强化/转职换算
│   ├── monsters.json        怪物/刷新权重
│   ├── economy.json         离线/体力/战斗/地图/分辨率
│   └── worlds.json          6 世界与点位
├── server/                服务端
│   ├── main.go              standalone 入口
│   ├── go.mod
│   ├── docker-compose.yml   Nakama + PostgreSQL
│   ├── nakama/config.yml    Nakama 配置
│   ├── schema.sql           11 张分表 + 扩展位
│   ├── nakama_module/       Nakama Go RPC 插件（build tag: nakama）
│   └── internal/
│       ├── game/            ★ 权威逻辑：配置/属性/战斗/离线
│       ├── store/           存档结构
│       └── net/             HTTP API
├── client/                Cocos Creator 3.8 项目（可直接用编辑器打开）
│   ├── package.json / tsconfig.json / settings/
│   └── assets/
│       ├── scenes/Main.scene   主场景（挂 GameRoot，UI 全代码驱动）
│       ├── scripts/
│       │   ├── core/        Protocol / Config / Network / State / Bootstrap
│       │   ├── systems/     BattlePresenter / ExploreController
│       │   └── cocos/       ★ 引擎适配层：GameRoot / 资源加载 / 序列帧动画 / views
│       ├── configs/         配置源
│       └── resources/       149 张像素资源 + connection_info.json + configs 副本
├── web/index.html         浏览器初跑验证页（零游戏逻辑，只发请求+表现）
├── scripts/               一键启动脚本
├── data/                  编译产物/日志/数据库
└── docs/
    ├── 改哪里能改什么.md     灵活修改指南
    └── protocol.md          通信协议
```

---

## 两种模式

| | standalone（默认） | Nakama 稳定栈 | Nakama + 权威插件 |
|---|---|---|---|
| 启动 | `bash scripts/start.sh` | `cd server && docker compose up -d` | `docker compose -f docker-compose.plugin.yml up -d` |
| 端口 | HTTP 8080 | 7350 / 7351 | 7350 / 7351 |
| 存档 | 磁盘 `data/saves/<ID>.json` | Nakama Storage | Nakama Storage |
| 提供 | 属性/战斗/离线/配置 + **存档落库 API** | 账号认证 + Storage | 账号 + Storage + **6 个权威 RPC** |
| 用途 | 立即验证核心逻辑与存档闭环 | 公网内测（开箱即起） | 需要服务端权威 RPC 时 |

> 插件是可选增强：Go plugin 对编译环境极敏感，须用官方 `nakama-pluginbuilder:3.24.0` 镜像构建（`scripts/build-plugin.sh`），否则 Nakama 会拒绝加载。**交付包不含预编译 .so**（本沙箱编译的 .so 与目标 Nakama 二进制 build ID 不符），请在实际部署机运行构建脚本生成。默认栈不含插件，稳定可起。

两者共用 `internal/game` + `internal/store` 权威逻辑，改一次两边生效。

### 存档闭环（已实测）

standalone 提供带落库的完整链路，Web 验证页已接入：

| 端点 | 作用 |
|------|------|
| `POST /api/character/new` | 创角并落盘 |
| `POST /api/state/load` | 从磁盘读档 |
| `POST /api/battle/fight` | 战斗结算 + 奖励入账 + 落库 |
| `POST /api/idle/finalize` | 离线结算 + 入账 + 推进 lastSeen + 落库 |
| `POST /api/transfer` | 转职 + 装备按新职业系数换算 + 落库 |
| `POST /api/battle/resolve`、`/api/idle/settle` | 纯结算（不落库，回放/验证用） |

Nakama 模式对应 6 个 RPC：`get_config / character_create / state_load / battle_resolve / idle_settle / transfer_class`。
RPC 从 session 取 userId（客户端不可伪造），角色属性以服务端存档为准，忽略客户端上传的等级/装备。
插件构建见 [`scripts/build-plugin.sh`](scripts/build-plugin.sh)（⚠️ Go plugin 须与 Nakama 二进制同 Go 版本）。

---

## 关键设计口径（与需求文档一致）

- **服务端权威**：战斗、离线结算、随机种子都在服务端；客户端只表现，防篡改。
- **玩家零输入**：战斗自动演出，技能栏是只读回显（4 已装 + 普攻）；决策在战前 BD。
- **可复现**：相同 seed 必得相同战报（已实测），便于 playtest 回放与 bug 复现。
- **无损转职**：装备随职业按系数自动换算（纯函数 `TransferEquip`）。
- **离线封顶 8h**：只认服务端时间戳，改系统时间无效（已实测）。
- **分表存档**：11 张逻辑表 + `ext_data` 扩展位，新系统零改表结构入库。
- **逻辑分辨率**：1536×1024（以原型图实物为准；地图设计 v0.4 的 1280×720 已在修订说明 R-01 标注待统一）。

---

## 数值说明

所有数值是 `[PLACEHOLDER]` 展示值，来自需求文档 tuning 表与地图设计第七节，**未经 playtest**。
调数值改 `configs/*.json`，不要改代码。详见 [`docs/改哪里能改什么.md`](docs/改哪里能改什么.md)。

---

## 本轮新增：Cocos 场景骨架 + 多人同步

### ① Cocos Creator 3.8 场景骨架

`client/` 现在是**可直接用 Cocos Creator 3.8 打开的项目**。UI 全部代码驱动，场景只需挂一个 `GameRoot`：

| 文件 | 作用 |
|------|------|
| `assets/scenes/Main.scene` | 主场景：GameRoot 节点 + 相机 |
| `assets/scripts/cocos/GameRoot.ts` | 入口：加载→连接→组装 HUD/世界/战斗→分派推送 |
| `assets/scripts/cocos/CocosResourceLoader.ts` | 引擎 resources 桥 |
| `assets/scripts/cocos/anim/PixelAnimator.ts` | 解析 animation_spec，序列帧播放 |
| `assets/scripts/cocos/views/*.ts` | HUD / 大世界 / 战斗 / 远端玩家 |

核心逻辑层（core/systems）保持纯 TS 不依赖引擎，可在纯 tsc 下独立编译。**已实测：全部 TS 编译 0 错误、场景 JSON 合法、脚本 UUID 引用一致。** 打开方式见 [`client/assets/scripts/cocos/README.md`](client/assets/scripts/cocos/README.md)。

### ② 大世界多人同步

`nakama_module/match.go` 实现 Match handler：同 worldId 玩家进同一线，服务端权威坐标、10Hz 广播快照、限速防瞬移（>600 单位/秒丢弃）、单线上限 40 人。客户端 `RemotePlayerLayer.ts` 承接广播渲染远端玩家。详见 [`docs/multiplayer-match.md`](docs/multiplayer-match.md)。

**已实测：插件编译通过（含 Match），防瞬移 6 用例 + 快照序列化单测全部 PASS。**

---

## 尚需补全（明确的边界）

这是"初跑骨架"，不是完整游戏。以下内容按需求文档范围留待后续迭代：
- 实机联调（需在实际部署机用 `build-plugin.sh` 重建含 Match 的插件并加载）
- 实时多人观战、世界 BOSS、公会、交易行（需求文档 Out of Scope）
- 数值平衡（需纸原型 + playtest 后填 configs）
- 公网联调的 Cloudflare 隧道脚本与密钥管理（沿用之前联机方案即可）

### 关于权威 RPC 插件（如实说明）

`nakama_module/` 已实现 6 个权威 RPC 的完整代码，`scripts/build-plugin.sh` 可用官方
`nakama-pluginbuilder:3.24.0` 镜像构建出 `plugins/zjcs.so`。但 Go plugin 机制要求插件与
Nakama 二进制的**标准库 build ID 完全一致**；本沙箱用官方镜像构建后仍报
`different version of package internal/goarch`，说明目标部署环境的 Nakama 二进制
使用了非公开的构建参数——这类问题需在**实际部署机**上按该二进制的构建环境重编译插件。
这**不影响**核心交付：standalone 模式已实现并实测全部存档闭环（含磁盘持久化），
纯 Nakama 稳定栈提供账号 + Storage，开箱即起。生产如需权威 RPC，请在实际部署机
用 `scripts/build-plugin.sh` 重新构建插件后改用 `docker-compose.plugin.yml`。
