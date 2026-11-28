# 新版六职业贴图（暂存）

本分支用于**暂存** 2026-11-28 制作的新版六职业美术，当前**未启用**。主线分支已回退到旧版贴图。

## 内容

- 六职业写实立绘：`web/res/characters/parts/char_*/char_*_standee.webp`（256×384）
- 程序动画：每职业 66 帧（`idle/walk/attack/cast/hit/die/victory/celebrate`，帧率与循环配置见 `actions.json`）
- 配套卡面：`web/res/characters/cards/` 与 `web/res/v3/characters/cards/`
- 对应提交：`bc8019b`（perf(assets): 角色帧转 WebP、动画懒加载）

## 标签

- `staged-new-character-art-20261128`

## 恢复方式

1. 将本分支的 `web/res/characters/parts/`、`cards/` 与卡面覆盖回主线。
2. 同步 `StandeeActor.js` / `creator.js` / `index.html` 的资源版本参数，避免缓存。
3. 确认 `app.js` 中的立绘朝向映射与各职业一致。

> 状态：暂存，未接入主线，不要直接部署。
