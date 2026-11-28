package game

import (
	"math"
	"testing"
)

// v1.9.85 技能与技能等级计入战力：base[档位] × kindWeight[类型] × SkillEffectMult(等级)。
func TestSkillPower(t *testing.T) {
	if err := Init("../../../configs"); err != nil {
		t.Fatal(err)
	}

	// 1) 单技能：火球术 = initial(60) × magic(1.0) × 1 级(1.0) = 60
	if got := SkillPower("m_fireball", 1); got != 60 {
		t.Fatalf("火球术 1 级战力应为 60，实际 %.0f", got)
	}
	// 治疗术 = initial(60) × heal(0.7) = 42
	if got := SkillPower("m_heal", 1); got != 42 {
		t.Fatalf("治疗术 1 级战力应为 42，实际 %.0f", got)
	}
	// 召唤 = advanced(120) × summon(1.0) = 120
	if got := SkillPower("wl_summon", 1); got != 120 {
		t.Fatalf("召唤 1 级战力应为 120，实际 %.0f", got)
	}

	// 2) 等级加成：6 级 = ×1.4（与 skillUpgrade.perLevel=0.08 同源）
	if got := SkillPower("m_fireball", 6); got != 84 {
		t.Fatalf("火球术 6 级战力应为 84（60×1.4），实际 %.0f", got)
	}
	// 等级缺省 / 0 / 负数都按 1 级算
	for _, lv := range []int{0, -3} {
		if got := SkillPower("m_fireball", lv); got != 60 {
			t.Fatalf("等级 %d 应按 1 级计 60，实际 %.0f", lv, got)
		}
	}

	// 3) 吸取 = advanced(120) × magic(1.0) = 120（v1.9.86 已恢复）；未知 id 不贡献、不 panic
	if got := SkillPower("wl_drain", 1); got != 120 {
		t.Fatalf("吸取 1 级战力应为 120，实际 %.0f", got)
	}
	if got := SkillPower("no_such_skill", 5); got != 0 {
		t.Fatalf("未知技能不应贡献战力，实际 %.0f", got)
	}

	// 4) 合计：术士四格 = 60 + 42 + 84 + 120 = 306；重复槽位只算一次
	ids := []string{"m_fireball", "m_heal", "wl_curse", "wl_summon"}
	if got := SkillPowerSum(ids, nil); got != 306 {
		t.Fatalf("术士四技能 1 级合计应为 306，实际 %.0f", got)
	}
	if got := SkillPowerSum(append(ids, "m_fireball", "m_fireball"), nil); got != 306 {
		t.Fatalf("重复技能应只计一次，实际 %.0f", got)
	}

	// 5) CalcStats 的总战力 = 装备/等级战力 + 技能战力
	av := Avatar{Level: 30, ClassID: "warlock"}
	bare := CalcStats(av).Power
	withSkills := CalcStats(Avatar{
		Level: 30, ClassID: "warlock", Skills: ids,
		SkillLv: map[string]int{"m_fireball": 6},
	}).Power
	// 火球从 60 涨到 84，其余不变 → 合计 330
	if want := bare + 330; math.Abs(withSkills-want) > 0.5 {
		t.Fatalf("带技能总战力应为 %.0f（裸装 %.0f + 技能 330），实际 %.0f", want, bare, withSkills)
	}
	t.Logf("Lv30 术士：裸装战力 %.0f → 四技能（火球6级）%.0f", bare, withSkills)
}

// v1.9.85 upgrade 后的战力量级：满级 20 级技能应显著高于 1 级，但不应压过装备/等级主体。
func TestSkillPowerLevelScaling(t *testing.T) {
	if err := Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	lv1 := SkillPowerSum([]string{"w_slash", "w_guard"}, map[string]int{})
	lv20 := SkillPowerSum([]string{"w_slash", "w_guard"}, map[string]int{"w_slash": 20, "w_guard": 20})
	// 20 级 = ×(1+0.08×19)=×2.52
	if want := math.Round(lv1 * 2.52); lv20 != want {
		t.Fatalf("20 级技能战力应为 %.0f（1 级的 2.52 倍），实际 %.0f", want, lv20)
	}
	t.Logf("战士双技能：1 级 %.0f → 20 级 %.0f", lv1, lv20)
}
