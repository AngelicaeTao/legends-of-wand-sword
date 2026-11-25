# Legends of Wand & Sword · 杖剑传说

服务端权威的 Web 放置手游（Playable 原型）：Go 单二进制权威服务 + 原生 Canvas 部件动画前端，单端口同源部署，支持多端接入。

当前初版归档版本：**v1.7.5**。

## 分支说明

本仓库按「代码 / 资源」分离存放，克隆时按需拉取对应分支：

| 分支 | 内容 | 体积 |
|---|---|---:|
| `code/v1.7.5` | 游戏代码：Go 服务端、Web 前端（JS/CSS/HTML）、配置文件、脚本、客户端骨架、交付文档 | 约 2.7 MB |
| `assets/v1.7.5` | 美术资源：按用途分类（地图 / 精灵 / 背景 / 卡面 / 图标 / 特效 / 旧版素材） | 约 8.7 MB |
| `main` | 仓库说明与分支导航（本页） | — |

### 拉取方式

```bash
# 代码
git clone -b code/v1.7.5 https://github.com/AngelicaeTao/legends-of-wand-sword.git

# 资源（放入同一工作目录，按 assets 分支 README 的回填映射还原）
git clone -b assets/v1.7.5 https://github.com/AngelicaeTao/legends-of-wand-sword.git assets
```

## 快速开始

```bash
cd server && go build -o ../data/zjcs-server .
cd .. && ./data/zjcs-server -addr :3000 -config configs -web web/index.html -res web/res
# 打开 http://localhost:3000
```

资源需从 `assets/v1.7.5` 回填到 `web/res/`，映射表见 assets 分支的 `README.md`。

## 技术概览

- **服务端**：Go，单二进制，HTTP + WebSocket，存档落 JSON；战斗/掉落/宝箱/装备/仓库/副本全部服务端权威
- **前端**：原生 JS + Canvas，部件化骨骼动画（parts/actions 配置驱动），单通道 UI 状态机
- **大世界**：6 个 15360×8640 世界，地形图 + 通行掩码硬碰撞；渲染按 1280×720 分块纹理 + 异步裁剪 + LRU，规避 GPU 纹理上限
- **多人**：WS 10Hz 快照，`worldId+channel` 分线（40 人/线），断线退避重连

## 版本

| 版本 | 内容 |
|---|---|
| v1.7.5 | 小地图重做（真实地形局部雷达）、底栏金框布局修正、文案统一 |
| v1.7.2 | 地形分块渲染 + 缓存策略修复，掉帧问题闭环 |
| v1.6 | 底部 5 入口导航重构（角色/副本/家园/公会/地图） |
