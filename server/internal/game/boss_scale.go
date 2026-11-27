package game

import "math"

// v1.9.53 图域首领动态强度
//
// 问题：大地图首领用固定 base+growth。Lv32 仅 775 血、单次打 34 伤害（玩家 2.3% 血量），
// 玩家一发火球 300+，9 回合结束且血量越打越多——完全不像首领。
// 而单纯把 base 调高也不行：实测同一套倍率下 Lv20 打 2 回合、Lv32 打 20 回合、Lv50 打 1 回合，
// 因为玩家输出取决于等级+装备+BD，与"世界等级"并不挂钩。
//
// 做法：锚定挑战者自身的属性结构，而不是怪物等级。
//
//	战力倍数 r = 玩家实际攻击力 / 同等级同职业裸装攻击力
//	  └ 装备品质、强化、职业系数、等级全部体现在这里（r=1 表示毫无装备）
//
//	血量 = 基准血量(等级曲线) × clamp(r,min,max)^hpExp × (1 + hpPerProgress·w)
//	  └ 强于同级基准的玩家，首领同步变厚；指数 <1 让攒装备仍有"更快击杀"的正反馈
//
//	攻击 = 反解伤害公式，使每击固定打掉玩家最大血量的 share
//	  └ 伤害公式：dmg = monATK × (1 - heroDEF/(heroDEF+100))
//	    所以 monATK = HP × share / (1 - heroDEF/(heroDEF+100))
//	  └ 战士高防、法师低防都得到同等比例的压迫感
//
// q=装备品质分、e=强化分、w=推进进度，与 r 一起构成给策划的调节旋钮。
func bossScale(req BattleRequest) (hpMul, atkOverride float64, ok bool) {
	cfg := C().Monsters.BossScaling
	if len(cfg) == 0 {
		return 1, 0, false
	}
	numOf := func(k string, def float64) float64 {
		if v, ok := cfg[k].(float64); ok {
			return v
		}
		return def
	}

	stats := CalcStats(req.Avatar)
	bare := CalcStats(Avatar{Level: req.Avatar.Level, ClassID: req.Avatar.ClassID})
	if bare.ATK <= 0 || stats.HP <= 0 {
		return 1, 0, false
	}

	q := qualityScore(req.Avatar.Equip)
	w := math.Max(0, math.Min(1, req.Progress))

	// 1) 血量倍率：装备相对强度 r 决定首领有多厚
	// hpRatioFree：把 r 超出 1 的前一段视为"基础装备/新手武器"豁免，不计入难度。
	// 否则 Lv1 白送的一把练习剑就能把 r 抬到 1.5，一级玩家要打 20+ 回合。
	r := stats.ATK / bare.ATK
	r = math.Max(1, r-numOf("hpRatioFree", 0.5))
	r = math.Max(numOf("hpRatioMin", 0.7), math.Min(numOf("hpRatioMax", 4.5), r))
	hpMul = math.Pow(r, numOf("hpExp", 0.85)) * (1 + numOf("hpPerProgress", 0.30)*w)

	// 2) 攻击：按"每击打掉玩家多少比例血量"反解，而不是按等级给固定攻击
	share := numOf("atkShare", 0.075) * (1 + numOf("atkPerQuality", 0.15)*q + numOf("atkPerProgress", 0.20)*w)
	share = math.Max(numOf("atkShareMin", 0.04), math.Min(numOf("atkShareMax", 0.16), share))
	defFactor := 1 - stats.DEF/(stats.DEF+100) // 玩家防护后的实际承伤比例
	if defFactor < 0.05 {
		defFactor = 0.05
	}
	atkOverride = stats.HP * share / defFactor

	return hpMul, atkOverride, true
}

// qualityScore 四件装备品质（0~7 档）归一化到 0~1；未穿戴的槽位按 0 计。
func qualityScore(equip []EquipItem) float64 {
	const maxSum = 4 * 8 // 四槽 × 最高品质 (quality+1)=8
	sum := 0
	for _, e := range equip {
		if !isEquipSlot(e.Slot) {
			continue
		}
		q := e.Quality
		if q < 0 {
			q = 0
		}
		if q > 7 {
			q = 7
		}
		sum += q + 1
	}
	if sum <= 0 {
		return 0
	}
	return math.Min(1, float64(sum)/maxSum)
}

func isEquipSlot(slot string) bool {
	switch slot {
	case "weapon", "helmet", "armor", "trinket":
		return true
	}
	return false
}
