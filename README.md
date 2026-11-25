# 杖剑传说 · 类放置手游（Playable 原型）

服务端权威的 Web 放置手游原型：Go 单二进制权威服务 + 原生 Canvas 部件动画前端，单端口同源部署，支持多端接入。

- 当前版本：**v1.7.5**（初版归档）
- 代码分支：`code/v1.7.5`（本分支）
- 资源分支：`assets/v1.7.5`（美术资源，按类别分目录）

## 目录结构

```
server/            Go 权威服务（cmd / internal: game 规则、net HTTP+WS、store 存档）
  ├ internal/game  战斗、掉落、经济、世界点位、配置加载
  ├ internal/net   HTTP API、WebSocket 分线同步、静态资源托管
  └ internal/store 账号/角色/背包/仓库/装备/副本 落库
configs/           配置驱动 JSON（worlds 世界与点位、economy 经济、tables 数值表）
web/
  ├ index.html     单页应用入口（单通道状态机 login→world→panel/modal）
  └ res/
      ├ js/        前端逻辑（app.js 主控、worldmap.js 大世界引擎、PartActor.js 部件动画）
      └ css/       样式
client/            Cocos Creator 3.8 客户端骨架（同构 Nakama 协议预留）
scripts/           世界生成、资源切图、构建辅助脚本
docs/              交付文档与实机验证截图
项目档案.md        项目全景（架构决策 / 里程碑 / 技术债 / 迭代记录）
```

> 美术资源不在本分支。请从 `assets/v1.7.5` 分支获取，按下方「资源回填」说明放回 `web/res/`。

## 构建与运行

```bash
# 1) 编译服务端（Go 1.21+）
cd server && go build -o ../data/zjcs-server .

# 2) 启动（单端口同源托管前端与资源）
cd .. && ./data/zjcs-server -addr :3000 -config configs -web web/index.html -res web/res
```

浏览器打开 `http://localhost:3000`，设备登录即可进入大世界。

## 资源回填（从 assets 分支还原）

资源分支按类别组织，回填到运行目录的对应位置：

| assets/v1.7.5 路径 | 回填到 | 说明 |
|---|---|---|
| `maps/` | `web/res/maps/` | 6 世界地形主图、通行掩码、拼接配置 |
| `sprites/characters/` | `web/res/v3/parts/` | 部件化角色包（parts.json / actions.json / 部位 PNG） |
| `sprites/monsters/` | `web/res/v3/parts/` | 部件化怪物包 |
| `sprites/items/` | `web/res/v3/parts/` | 部件化宝箱包 |
| `backgrounds/` | `web/res/v3/backgrounds/` | 战斗/探索背景 |
| `portraits/cards/` | `web/res/characters/cards/` | 职业卡面 |
| `icons/` | `web/res/icons/` | UI 图标 |
| `fx/` | `web/res/fx/` | 战斗特效帧 |
| `frames/legacy/` | `web/res/characters/` | 旧版逐帧角色（兼容保留） |
| `legacy/monsters/` | `web/res/monsters/` | 旧版逐帧怪物（兼容保留） |
| `legacy/items/` | `web/res/items/` | 旧版逐帧宝箱（兼容保留） |

## 关键技术点

- **世界**：6 个 15360×8640 大世界，AI 地形图 + 通行掩码硬碰撞（9 点采样），最远点采样 16 产出点/世界。
- **渲染性能**：地形按 1280×720 分块纹理 + `createImageBitmap` 异步裁剪 + LRU(36) + 后台预热，规避 GPU 单纹理上限（实测 60FPS）。
- **同步**：WebSocket 10Hz 快照，`worldId+channel` 分线（40 人/线）+ 断线 1/2/4/8s 退避重连。
- **服务端权威**：宝箱档位、战斗结算、BD 配置、装备拆装、仓库存取、副本次数均由服务端判定。

## 版本

| 版本 | 内容 |
|---|---|
| v1.7.5 | 小地图重做（真实地形局部雷达）、底栏金框布局修正、文案统一 |
| v1.7.2 | 地形分块渲染 + 缓存策略修复，掉帧问题闭环 |
| v1.6 | 底部 5 入口导航重构（角色/副本/家园/公会/地图） |
