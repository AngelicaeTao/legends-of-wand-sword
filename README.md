# 美术资源库 · 杖剑传说 v1.9.44

本分支只存放美术资源，**不含任何代码**。代码见 `code/v1.9.44` 分支。

- 总计：**286 个文件 / 约 7.8 MB**
- 组织方式：按「用途」分类，每类一个顶层目录；部件化资源按资源包再分子目录。
- 回填运行目录的对应关系见文末「回填映射」。

## 目录清单

| 目录 | 文件数 | 用途 |
|---|---:|---|
| `maps/` | 24 | 6 个世界的地形主图、通行掩码、拼接配置 |
| `sprites/characters/` | 12 | 部件化角色包（主角法师） |
| `sprites/monsters/` | 65 | 部件化怪物包（7 类） |
| `sprites/items/` | 25 | 部件化宝箱包（5 档：木/银/金/钻石/水晶） |
| `backgrounds/` | 6 | 战斗/探索背景（6 张） |
| `portraits/cards/` | 6 | 职业卡面（战士/骑士/法师/贤者/术士/斗士） |
| `icons/` | 10 | UI 图标（旧版 10） |
| `icons/v3/` | 10 | UI 图标（v3 版，运行时实际引用） |
| `fx/` | 36 | 战斗特效帧（暴击/普攻/经验/金币/治疗/闪避） |
| `frames/legacy/` | 20 | 旧版逐帧角色（idle/walk/attack/hit/die） |
| `legacy/monsters/` | 56 | 旧版逐帧怪物（4 类 × 各动作） |
| `legacy/items/` | 15 | 旧版逐帧宝箱（3 档 × 开箱动画） |

## 目录明细

```
maps/
  W1..W6_full.png            世界地形主图（15360×8640）
  W1..W6_full_mask.png      全尺寸通行掩码
  W1..W6_mask_small.png     降采样掩码（1920×1080，运行时碰撞用）
  W1..W6_stitch_config.json 世界拼接配置（节点/通道/传送门语义坐标）

sprites/
  characters/
    char_hero_mage/          parts.json + actions.json + 各部位 PNG
  monsters/
    mon_slime/               史莱姆
    mon_skeleton/            骷髅兵
    mon_bat/                 蝙蝠
    mon_boss_beetle/         甲虫将军（BOSS）
    mon_slime_toxic/         毒液史莱姆（PIL 调色衍生）
    mon_skeleton_archer/     骷髅弓手（PIL 调色衍生）
    mon_bat_crimson/         血蝙蝠（PIL 调色衍生）
  items/
    item_chest_wood/         木箱
    item_chest_silver/       银箱
    item_chest_gold/         金箱
    item_chest_diamond/      钻石箱（v1.9.42 新增，PIL 调色衍生·青蓝）
    item_chest_crystal/      水晶箱（v1.9.42 新增，PIL 调色衍生·紫粉）

backgrounds/                 战斗与 6 世界探索背景
portraits/cards/             职业卡面
icons/ + icons/v3/           UI 图标
fx/                          特效序列帧
frames/legacy/               旧版逐帧角色（兼容保留）
legacy/monsters/             旧版逐帧怪物（兼容保留）
legacy/items/                旧版逐帧宝箱（兼容保留）
```

## 资源包格式（部件化）

`sprites/**/<bundle>/` 每个包包含：

- `parts.json`：部件清单，含每张图的文件、z 序、锚点偏移
- `actions.json`：动作状态机，`states[idle/walk/attack/hit/die]` 下按关键帧描述各部件 `dx/dy/rot/sx/sy/alpha/flash`
- 各部位 PNG：可独立变换的图层（头/身/臂/腿/武器等），支持换装与程序补间

## 回填映射（还原到运行目录）

| 本分支路径 | 回填到 |
|---|---|
| `maps/` | `web/res/maps/` |
| `sprites/characters/*` | `web/res/v3/parts/` |
| `sprites/monsters/*` | `web/res/v3/parts/` |
| `sprites/items/*` | `web/res/v3/parts/` |
| `backgrounds/` | `web/res/v3/backgrounds/` |
| `portraits/cards/` | `web/res/characters/cards/` |
| `icons/` | `web/res/icons/` |
| `icons/v3/` | `web/res/v3/icons/` |
| `fx/` | `web/res/fx/` |
| `frames/legacy/` | `web/res/characters/` |
| `legacy/monsters/` | `web/res/monsters/` |
| `legacy/items/` | `web/res/items/` |

## 已知资源问题（待修）

- `maps/*_full.png` 存在大片「棋盘格底 + 中央蓝色圆」区域（生成时透明底/水塘被烘焙进图），而掩码把蓝圆标为可走，导致视觉上角色会站在蓝色圆内。属素材质量问题，需重出地形资源或按掩码修正绘制。

## v1.9.2 新增（2026-11-26）
- `maps/W*_full_half.png`：7680×4320 地形图（原 15360 图 NEAREST 折半，用于 v1.8+ 客户端）
- `maps/W*_mask_half.png`：960×540 通行掩码（1px = 8 世界单位）
- 原 `W*_full.png` / `W*_mask_small.png` 保留为高分辨率母版，未删除
- 地形图已做去紫处理（原图未绘制区的洋红色块 → 邻域扩散填色）

## v1.9.42 新增（2026-11-26）
- `sprites/items/item_chest_diamond/`：钻石箱（金箱 PIL 调色·青蓝系，含 parts.json/actions.json/PNG 全套，PNG 随 bundle 重命名）
- `sprites/items/item_chest_crystal/`：水晶箱（金箱 PIL 调色·紫粉系，同上）
- 已在客户端 `animation_spec.json` 注册 bundles，预载列表同步
