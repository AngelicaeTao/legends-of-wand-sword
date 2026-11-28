package game

import "math"

// PassiveBonus 是已装配被动技能在战斗中生效的「特效型」效果汇总（v1.9.89）。
//
// 为什么不塞进 Stats：属性型被动（hpPct/atkPct/defPct/critAdd/spdAdd）在 CalcStats 里
// 已经叠加到五维上，战斗读 Stats 即可；但吸血、减伤、反伤、开场护盾、治疗/护盾增幅、
// 冷却缩减、低血增伤、追加攻击、周期回复、按类型增伤、降攻强化这些没有对应的属性位，
// 统一在这里汇总成一个只读快照，供 battle.go（单人）与 party_battle.go（组队）共用，
// 保证两端同一套口径。
//
// 数值上限统一在 CalcPassiveBonus 里 clamp，避免高等级被动把机制叠到免疫/破防。
type PassiveBonus struct {
	Lifesteal   float64            // 所有伤害额外吸血比例
	DR          float64            // 常驻受伤减免
	Thorns      float64            // 受击反伤比例
	OpenShield  float64            // 开场护盾（×攻击力）
	HealBoost   float64            // 治疗量增幅
	ShieldBoost float64            // 护盾量增幅
	CDR         float64            // 全技能冷却缩减
	LowHPThresh float64            // 低血阈值（生命占比）
	LowHPDmg    float64            // 低于阈值时的增伤
	ExtraEvery  int                // 每 N 个英雄回合追加一击
	ExtraPower  float64            // 追加一击的伤害系数
	RegenEvery  int                // 每 N 个英雄回合回复一次
	RegenPct    float64            // 每次回复的最大生命占比
	KindDmg     map[string]float64 // 按技能类型增伤（physical/magic/debuff/heal/shield…）
	DebuffBoost float64            // 降攻类减益的额外增幅
}

// Empty 判断是否为「无任何被动特效」，战斗里据此跳过钩子，零被动时保持原有性能与行为。
func (p PassiveBonus) Empty() bool {
	return p.Lifesteal == 0 && p.DR == 0 && p.Thorns == 0 && p.OpenShield == 0 &&
		p.HealBoost == 0 && p.ShieldBoost == 0 && p.CDR == 0 && p.LowHPDmg == 0 &&
		p.ExtraEvery == 0 && p.RegenEvery == 0 && len(p.KindDmg) == 0 && p.DebuffBoost == 0
}

// CalcPassiveBonus 把被动 id 列表汇总成特效快照（v1.9.89）。
// 等级加成复用 SkillEffectMult（每级 +8%），与主动技能、被动战力三处同源。
// 未配置/非被动 id 静默跳过，脏存档不会让战斗崩。
func CalcPassiveBonus(ids []string, levels map[string]int) PassiveBonus {
	c := C()
	pb := PassiveBonus{KindDmg: map[string]float64{}}
	seen := make(map[string]bool, len(ids))

	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		s, ok := c.SkillByID(id)
		if !ok || s.Kind != "passive" {
			continue
		}
		m := SkillEffectMult(levels[id])

		pb.Lifesteal += s.LifestealAdd * m
		pb.DR += s.DRAdd * m
		pb.Thorns += s.Thorns * m
		pb.OpenShield += s.OpenShield * m
		pb.HealBoost += s.HealBoost * m
		pb.ShieldBoost += s.ShieldBoost * m
		pb.CDR += s.CDR * m
		pb.DebuffBoost += s.DebuffBoost * m

		// 低血增伤：多个被动只取最强的一档（阈值与增伤成对，取增伤最高者）
		if s.LowHPDmg > 0 && s.LowHPDmg*m > pb.LowHPDmg {
			pb.LowHPDmg = s.LowHPDmg * m
			pb.LowHPThresh = s.LowHPThresh
		}
		// 追加攻击：间隔取最短、系数累加（配置里每职业最多一个）
		if s.ExtraEvery > 0 && s.ExtraPower > 0 {
			if pb.ExtraEvery == 0 || s.ExtraEvery < pb.ExtraEvery {
				pb.ExtraEvery = s.ExtraEvery
			}
			pb.ExtraPower += s.ExtraPower * m
		}
		// 周期回复：同上
		if s.RegenEvery > 0 && s.RegenPct > 0 {
			if pb.RegenEvery == 0 || s.RegenEvery < pb.RegenEvery {
				pb.RegenEvery = s.RegenEvery
			}
			pb.RegenPct += s.RegenPct * m
		}
		for k, v := range s.KindDmg {
			pb.KindDmg[k] += v * m
		}
	}

	// 上限：防止高等级叠加后出现「免疫」「破防」「无限吸血」
	pb.Lifesteal = math.Min(pb.Lifesteal, 1.0)
	pb.DR = math.Min(pb.DR, 0.75)
	pb.Thorns = math.Min(pb.Thorns, 1.0)
	pb.HealBoost = math.Min(pb.HealBoost, 2.0)
	pb.ShieldBoost = math.Min(pb.ShieldBoost, 2.0)
	pb.CDR = math.Min(pb.CDR, 0.5)
	pb.DebuffBoost = math.Min(pb.DebuffBoost, 0.5)
	pb.RegenPct = math.Min(pb.RegenPct, 0.5)
	for k, v := range pb.KindDmg {
		pb.KindDmg[k] = math.Min(v, 1.0)
	}
	return pb
}

// KindDmgOf 取某技能类型的增伤，未配置返回 0（nil map 安全）。
func (p PassiveBonus) KindDmgOf(kind string) float64 {
	if p.KindDmg == nil {
		return 0
	}
	return p.KindDmg[kind]
}

// LowHPMult 低血增伤系数：生命占比 ≤ 阈值时返回 1+LowHPDmg，否则 1。
func (p PassiveBonus) LowHPMult(hpNow, hpMax float64) float64 {
	if p.LowHPThresh <= 0 || p.LowHPDmg <= 0 || hpMax <= 0 {
		return 1
	}
	if hpNow/hpMax <= p.LowHPThresh {
		return 1 + p.LowHPDmg
	}
	return 1
}
