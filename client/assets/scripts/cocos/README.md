# Cocos Creator 3.8 客户端骨架

## 这是什么

这是初跑版的**引擎适配层**。核心业务逻辑（`../core`、`../systems`）是纯 TypeScript，不依赖引擎；本目录只负责把它们接到 Cocos 的渲染与资源系统。

**设计原则**：UI 全部代码驱动 —— 场景里只需要一个挂 `GameRoot` 的根节点，其余界面（HUD / 大世界 / 战斗 / 远端玩家）都在运行时用代码创建。好处：

- 场景文件极简，不依赖脆弱的手摆节点 UUID 引用；
- 换分辨率、换皮肤只改代码里的常量，不用在编辑器里重摆；
- 逻辑层可在纯 tsc / Web 下独立编译验证。

## 文件结构

| 文件 | 职责 |
|------|------|
| `GameRoot.ts` | 主入口组件，挂场景根节点；加载→连接→组装视图→分派推送 |
| `CocosResourceLoader.ts` | 唯一接触引擎 `resources` API 的桥：连接配置/游戏配置/帧图片 |
| `anim/PixelAnimator.ts` | 解析 `animation_spec.json` 的 `file_pattern`，序列帧播放 |
| `views/HUDView.ts` | 顶部金币/钻石/体力/等级 + 底部只读技能栏 |
| `views/WorldView.ts` | 大世界：背景、主角、虚拟摇杆、遇怪弹窗（接 ExploreController） |
| `views/BattleView.ts` | 战斗：英雄/怪物序列帧、HP、特效、逐回合日志（接 BattlePresenter） |
| `RemotePlayerLayer.ts` | 远端玩家渲染（接 ② 的 `player_move` 广播） |

## 如何在编辑器打开

1. 用 **Cocos Creator 3.8** 打开 `client/` 目录（它是项目根，含 `package.json`、`assets/`、`settings/`）。
2. 首次打开会导入资源并生成 `.meta`（图片/JSON 的 meta 让编辑器自动生成即可）。
3. 打开 `assets/scenes/Main.scene`：里面已有 `GameRoot` 节点（挂 `GameRoot.ts`）和 `Main Camera`。
4. 点预览：会加载 `resources/connection_info.json` 里的地址。

> **兜底**：若某版本 Cocos 把场景里的脚本组件显示为 `Missing`（脚本 UUID 反序列化差异），删除该组件、把 `GameRoot.ts` 重新拖到根节点即可。因为 UI 全代码驱动，无需手摆任何节点，30 秒恢复。

## 连接配置

`assets/resources/connection_info.json`：

| 字段 | 说明 | 默认值 |
|------|------|--------|
| `httpBase` | standalone 权威服务地址，提供 `/api/config` 等 | `http://127.0.0.1:8080` |
| `host` / `port` | Nakama WebSocket 地址 | `127.0.0.1` / `7350` |
| `serverKey` | Nakama server key | `defaultkey` |

发布时由联机脚本覆盖此文件，**不要硬编码公网地址**。

## 与服务端的关系

- 战斗/离线/转职/存档全部服务端权威，客户端只发意图、收快照/战报；
- `Protocol.ts` 里的 `move` / `player_move` 对应 ② 的 Nakama Match handler 位置同步；
- 设备登录 ID 存 `localStorage`，重连后以服务端快照为准。
