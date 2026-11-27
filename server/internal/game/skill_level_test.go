package game

// v1.9.56 技能等级加成验证：同一颗种子下，技能等级越高，单次伤害/治疗越高。
// 加成规则：每级 +8%（SkillEffectMult），1 级无加成，避免"升级反而变弱"的观感。
// 注意：这里比的是"单次施放"的数值，不是全场累计——等级高会更快打死怪，
// 累计伤害反而可能更少，用总量验证会得出误判。

import "testing"

func TestSkillLevelBoost(t *testing.T) {
	if err := Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	req := BattleRequest{
		UserID: "t_skill_dmg", Avatar: Avatar{Level: 10, ClassID: "warrior"},
		BD: []string{"w_slash"}, MonsterID: "slime", MonsterLv: 10, Seed: 20261127,
	}
	firstHit := func(rep BattleReport) float64 {
		for _, r := range rep.Rounds {
			if r.Actor == "hero" && r.Action == "w_slash" && r.Damage > 0 {
				return r.Damage
			}
		}
		return 0
	}
	d1 := firstHit(ResolveBattle(req))
	req.SkillLv = map[string]int{"w_slash": 6} // 1 + 0.08×5 = 1.40 倍
	d6 := firstHit(ResolveBattle(req))
	if d1 <= 0 || d6 <= 0 {
		t.Fatalf("两场都应有重斩伤害：lv1=%v lv6=%v", d1, d6)
	}
	ratio := d6 / d1
	if ratio < 1.30 || ratio > 1.50 {
		t.Fatalf("6 级重斩单次伤害应约为 1 级的 1.40 倍，实际 %.2f（lv1=%.1f lv6=%.1f）", ratio, d1, d6)
	}

	// 倍率口径
	if m := SkillEffectMult(6); m < 1.3999 || m > 1.4001 {
		t.Fatalf("6 级倍率应为 1.40，实际 %v", m)
	}
	if m := SkillEffectMult(0); m != 1 {
		t.Fatalf("0/缺省等级应按 1 级处理，实际 %v", m)
	}
}

func TestSkillLevelHealBoost(t *testing.T) {
	if err := Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	// 开局残血：治疗不会被"满血上限"截断，才能看出等级差异
	req := BattleRequest{
		UserID: "t_skill_heal", Avatar: Avatar{Level: 10, ClassID: "mage"},
		BD: []string{"m_heal"}, MonsterID: "slime", MonsterLv: 1, Seed: 777, StartHP: 1,
	}
	firstHeal := func(rep BattleReport) float64 {
		for _, r := range rep.Rounds {
			if r.Actor == "hero" && r.Heal > 0 {
				return r.Heal
			}
		}
		return 0
	}
	h1 := firstHeal(ResolveBattle(req))
	req.SkillLv = map[string]int{"m_heal": 11} // 1 + 0.08×10 = 1.80 倍
	h11 := firstHeal(ResolveBattle(req))
	if h1 <= 0 || h11 <= 0 {
		t.Fatalf("两场都应有治疗量：lv1=%v lv11=%v", h1, h11)
	}
	ratio := h11 / h1
	if ratio < 1.7 || ratio > 1.9 {
		t.Fatalf("11 级治疗术首跳应约为 1 级的 1.80 倍，实际 %.2f（lv1=%.1f lv11=%.1f）", ratio, h1, h11)
	}
}
