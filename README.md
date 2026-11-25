# 美术资源库 · 杖剑传说 v1.7.5

本分支只存放美术资源，**不含任何代码**。代码见 `code/v1.7.5` 分支。

- 总计：**239 个文件 / 约 8.7 MB**
- 组织方式：按「用途」分类，每类一个顶层目录；部件化资源按资源包再分子目录。
- 回填运行目录的对应关系见文末「回填映射」。

## 目录清单

| 目录 | 文件数 | 体积 | 用途 |
|---|---:|---:|---|
| `maps/` | 24 | 7.7 MB | 6 个世界的地形主图、通行掩码、拼接配置 |
| `sprites/characters/` | 12 | 12 KB | 部件化角色包（主角法师） |
| `sprites/monsters/` | 39 | 31 KB | 部件化怪物包（4 类） |
| `sprites/items/` | 15 | 9.6 KB | 部件化宝箱包（3 档） |
| `backgrounds/` | 6 | 37 KB | 战斗/探索背景（6 张） |
| `portraits/cards/` | 6 | 12 KB | 职业卡面（战士/骑士/法师/贤者/术士/斗士） |
| `icons/` | 10 | 6.6 KB | UI 图标（背包/金币/公会/角色/地图等） |
| `fx/` | 36 | 26 KB | 战斗特效帧（暴击/普攻/经验/金币，各 6 帧） |
| `frames/legacy/` | 20 | 36 KB | 旧版逐帧角色（idle/walk/attack/hit/die ×4 帧） |
| `legacy/monsters/` | 56 | 55 KB | 旧版逐帧怪物（4 类 × 各动作） |
| `legacy/items/` | 15 | 11 KB | 旧版逐帧宝箱（3 档 × 开箱动画） |

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
  items/
    item_chest_wood/         木箱
    item_chest_silver/       银箱
    item_chest_gold/         金箱

backgrounds/                 战斗与 6 世界探索背景
portraits/cards/             职业卡面
icons/                       UI 图标
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
| `fx/` | `web/res/fx/` |
| `frames/legacy/` | `web/res/characters/` |
| `legacy/monsters/` | `web/res/monsters/` |
| `legacy/items/` | `web/res/items/` |

## 已知资源问题（待修）

- `maps/*_full.png` 存在大片「棋盘格底 + 中央蓝色圆」区域（生成时透明底/水塘被烘焙进图），而掩码把蓝圆标为可走，导致视觉上角色会站在蓝色圆内。属素材质量问题，需重出地形资源或按掩码修正绘制。
