// Package game · v1.9.93 具名材料掉落链（秘银 / 蓝宝石 / 精铁 / 钢锭 / 皮革 / 布料）。
//
// 背景：这六种具名材料在 v1.9.93 之前只有「创角一次性发放」（store.NewPlayer）这一个
// 来源，全链路没有其他产出点 —— 玩家手里的存量只减不增，材料体系名存实亡。
// 本文件把四个来源接到同一张权重表（configs/economy.json 的 drop.namedMaterial）：
//
//  1. 打怪：普通 / 精英 / 首领三档各自概率，精英与首领多 roll 几次
//  2. 开箱：按宝箱档位给概率与 roll 次数（木箱也有，只是很低）
//  3. 公会材料包：公会币兑换，除固定 30 强化材料外随机再给若干份具名材料
//  4. 组队副本通关：按队伍倍率给 roll 次数
//
// 掉落结果统一由 store.AddNamedMaterials 写入 ext.bag（背包·可回收物品），
// 与初始「秘银」同处一格，玩家在商店可按 economy.shop.sell.items 的专价回收。
package game

import (
	"math/rand"
	"sort"
)

// NamedMaterialDrop 一次具名材料掉落。字段直接嵌进战报 / 接口响应，
// 前端拿到 id+name+qty 即可渲染，不需要再查配置表。
type NamedMaterialDrop struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Qty  int    `json:"qty"`
}

// namedMatRow 解析后的一行掉落权重（权重越大越常见）。
type namedMatRow struct {
	id     string
	weight int
	lo, hi int // 数量区间，闭区间
}

// namedMatRoot 取 economy.drop.namedMaterial 整段；缺失时返回空 map，调用方按「不掉」处理。
func namedMatRoot() map[string]any {
	drop := C().Economy.Drop
	if drop == nil {
		return map[string]any{}
	}
	root, _ := drop["namedMaterial"].(map[string]any)
	if root == nil {
		return map[string]any{}
	}
	return root
}

// namedMatSection 取某个来源的配置段（monster / chest / guildPack / partyDungeon）。
func namedMatSection(key string) map[string]any {
	sec, _ := namedMatRoot()[key].(map[string]any)
	if sec == nil {
		return map[string]any{}
	}
	return sec
}

// namedNum 读配置数字：键存在就用它的值（允许显式写 0 表示关闭），
// 缺失才回落到 def。用 num() 兼容 float64/int 两种反序列化结果。
func namedNum(m map[string]any, key string, def float64) float64 {
	if v, ok := m[key]; ok {
		return num(v)
	}
	return def
}

// namedMatTable 解析掉落表。
// 排序规则：权重升序 + id 字典序 —— 权重小的稀有材料排在前面，且顺序完全确定
// （map 遍历顺序随机，若不排序同一个 seed 会 roll 出不同结果，战报就无法复现）。
func namedMatTable() []namedMatRow {
	tbl, _ := namedMatRoot()["table"].(map[string]any)
	if len(tbl) == 0 {
		return nil
	}
	rows := make([]namedMatRow, 0, len(tbl))
	for id, raw := range tbl {
		if id == "" || id[0] == '_' { // 跳过 _note 之类说明字段
			continue
		}
		row, _ := raw.(map[string]any)
		if row == nil {
			continue
		}
		w := int(namedNum(row, "weight", 0))
		if w <= 0 {
			continue
		}
		lo, hi := 1, 1
		if arr, ok := row["qty"].([]any); ok && len(arr) >= 2 {
			lo, hi = int(num(arr[0])), int(num(arr[1]))
			if lo < 1 {
				lo = 1
			}
			if hi < lo {
				hi = lo
			}
		}
		rows = append(rows, namedMatRow{id: id, weight: w, lo: lo, hi: hi})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].weight != rows[j].weight {
			return rows[i].weight < rows[j].weight
		}
		return rows[i].id < rows[j].id
	})
	return rows
}

// rollNamedMaterials 按权重表 roll rolls 次，同种材料自动合并成一条。
// rng 为 nil 或表为空时返回 nil（调用方无需判空数组）。
func rollNamedMaterials(rolls int, rng *rand.Rand) []NamedMaterialDrop {
	if rolls <= 0 || rng == nil {
		return nil
	}
	rows := namedMatTable()
	if len(rows) == 0 {
		return nil
	}
	total := 0
	for _, r := range rows {
		total += r.weight
	}
	if total <= 0 {
		return nil
	}
	out := []NamedMaterialDrop{}
	idx := map[string]int{}
	for i := 0; i < rolls; i++ {
		pick := rng.Intn(total)
		row := rows[0]
		for _, r := range rows {
			if pick < r.weight {
				row = r
				break
			}
			pick -= r.weight
		}
		qty := row.lo
		if row.hi > row.lo {
			qty += rng.Intn(row.hi - row.lo + 1)
		}
		if qty < 1 {
			qty = 1
		}
		if k, ok := idx[row.id]; ok {
			out[k].Qty += qty
			continue
		}
		idx[row.id] = len(out)
		out = append(out, NamedMaterialDrop{ID: row.id, Name: row.id, Qty: qty})
	}
	return out
}

// RollMonsterMaterials 打怪掉落（胜利时调用）。
// elite 直接传 BattleRequest.Elite 原值（""/elite/rareElite/boss），boss 传 mon.IsBoss：
// 首领判定优先于精英，避免「BOSS 传成 elite」时按低档概率发奖。
func RollMonsterMaterials(elite string, boss bool, rng *rand.Rand) []NamedMaterialDrop {
	if rng == nil {
		return nil
	}
	cfg := namedMatSection("monster")
	chance := namedNum(cfg, "chanceNormal", 0.08)
	rolls := int(namedNum(cfg, "rollsNormal", 1))
	switch {
	case boss || elite == "boss":
		chance = namedNum(cfg, "chanceBoss", 1.0)
		rolls = int(namedNum(cfg, "rollsBoss", 2))
	case elite != "": // elite / rareElite
		chance = namedNum(cfg, "chanceElite", 0.30)
		rolls = int(namedNum(cfg, "rollsElite", 2))
	}
	if chance <= 0 || rng.Float64() >= chance {
		return nil
	}
	return rollNamedMaterials(rolls, rng)
}

// RollChestMaterials 开箱掉落。tier 取 wood/silver/gold/diamond/crystal，
// 未知或空档位按木箱处理（与 chestOpen 的兜底语义一致）。
func RollChestMaterials(tier string, rng *rand.Rand) []NamedMaterialDrop {
	if rng == nil {
		return nil
	}
	cfg := namedMatSection("chest")
	if cfg == nil || len(cfg) == 0 {
		return nil
	}
	row, _ := cfg[tier].(map[string]any)
	if row == nil {
		row, _ = cfg["wood"].(map[string]any)
	}
	if row == nil {
		return nil
	}
	if chance := namedNum(row, "chance", 0.1); chance <= 0 || rng.Float64() >= chance {
		return nil
	}
	return rollNamedMaterials(int(namedNum(row, "rolls", 1)), rng)
}

// RollGuildPackMaterials 公会材料包（150 公会币）随机具名材料，必给、不给空。
func RollGuildPackMaterials(rng *rand.Rand) []NamedMaterialDrop {
	cfg := namedMatSection("guildPack")
	return rollNamedMaterials(int(namedNum(cfg, "rolls", 2)), rng)
}

// RollPartyDungeonMaterials 组队副本通关掉落：按配置概率触发，一次给 rolls 份。
func RollPartyDungeonMaterials(rng *rand.Rand) []NamedMaterialDrop {
	if rng == nil {
		return nil
	}
	cfg := namedMatSection("partyDungeon")
	if chance := namedNum(cfg, "chance", 0.6); chance <= 0 || rng.Float64() >= chance {
		return nil
	}
	return rollNamedMaterials(int(namedNum(cfg, "rolls", 2)), rng)
}
