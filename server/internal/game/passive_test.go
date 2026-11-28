package game

import (
	"math"
	"testing"
)

// v1.9.89 被动技能：4 个转职职业各 4 个，配置完整、字段有效、池子可查。
func TestPassiveConfigComplete(t *testing.T) {
	if err := Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"fighter": {"fp_swift", "fp_frenzy", "fp_bloodlust", "fp_ironfist"},
		"knight":  {"kp_bulwark", "kp_thorns", "kp_aegis_master", "kp_guardian"},
		"sage":    {"sp_grace", "sp_faith", "sp_haste", "sp_sanctuary"},
		"warlock": {"wp_bloodpact", "wp_darkness", "wp_frailty", "wp_void"},
	}
	for cls, ids := range want {
		pool := C().PassivePool(cls)
		if len(pool) != 4 {
			t.Fatalf("%s 被动池应有 4 个，实际 %d：%v", cls, len(pool), pool)
		}
		for _, id := range ids {
			if !containsID(pool, id) {
				t.Fatalf("%s 被动池缺少 %s：%v", cls, id, pool)
			}
			s, ok := C().SkillByID(id)
			if !ok {
				t.Fatalf("被动 %s 未配置", id)
			}
			if s.Kind != "passive" {
				t.Fatalf("%s 的 kind 应为 passive，实际 %s", id, s.Kind)
			}
			if s.ClassID != cls {
				t.Fatalf("%s 归属应为 %s，实际 %s", id, cls, s.ClassID)
			}
			if s.Desc == "" {
				t.Fatalf("%s 缺少 desc（界面直接展示它）", id)
			}
			// 每个被动至少要有一个真正生效的字段，否则等于空技能
			if !hasAnyEffect(s) {
				t.Fatalf("%s 没有任何生效字段（属性或战斗特效全为 0）", id)
			}
		}
	}
	// 一阶职业（战士/法师）没有被动，界面据此隐藏被动区
	for _, cls := range []string{"warrior", "mage"} {
		if n := len(C().PassivePool(cls)); n != 0 {
			t.Fatalf("%s 不应有被动池，实际 %d 个", cls, n)
		}
	}
	if n := PassiveSlotMax(); n != 2 {
		t.Fatalf("被动槽位应为 2，实际 %d", n)
	}
	t.Log("4 职业 × 4 被动配置完整，一阶职业无被动 ✓")
}

func containsID(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func hasAnyEffect(s SkillDef) bool {
	if s.HPPct != 0 || s.ATKPct != 0 || s.DEFPct != 0 || s.CritAdd != 0 || s.SpdAdd != 0 {
		return true
	}
	if s.LifestealAdd != 0 || s.DRAdd != 0 || s.Thorns != 0 || s.OpenShield != 0 {
		return true
	}
	if s.HealBoost != 0 || s.ShieldBoost != 0 || s.CDR != 0 || s.DebuffBoost != 0 {
		return true
	}
	if s.LowHPDmg != 0 || s.ExtraEvery != 0 || s.RegenEvery != 0 || len(s.KindDmg) > 0 {
		return true
	}
	return false
}

// v1.9.89 被动战力：只有固定档位分（base.passive × kindWeight.passive × 等级系数），
// 属性增幅不再二次计分，避免「装一个 +15% 生命的被动，战力和属性双涨」的虚高。
func TestPassivePowerFixedScore(t *testing.T) {
	if err := Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	// 单被动 1 级：80 × 1.0 × 1.0 = 80
	if p := PassivePowerSum([]string{"kp_bulwark"}, nil); p != 80 {
		t.Fatalf("单个 1 级被动战力应为 80，实际 %.1f", p)
	}
	// 两个被动：160
	if p := PassivePowerSum([]string{"kp_bulwark", "kp_thorns"}, nil); p != 160 {
		t.Fatalf("两个 1 级被动战力应为 160，实际 %.1f", p)
	}
	// 6 级：80 × (1+0.08×5) = 112
	// 8 级：80 × (1+0.08×7) = 124.8 → 四舍五入 125
	if p := PassivePowerSum([]string{"kp_bulwark"}, map[string]int{"kp_bulwark": 6}); p != 112 {
		t.Fatalf("6 级被动战力应为 112，实际 %.1f", p)
	}
	if p := PassivePowerSum([]string{"kp_bulwark"}, map[string]int{"kp_bulwark": 8}); p != 125 {
		t.Fatalf("8 级被动战力应为 125，实际 %.1f", p)
	}
	// 脏数据：主动技能塞进被动槽不得计分（否则可与主动槽重复计算）
	if p := PassivePowerSum([]string{"w_slash", "m_fireball"}, nil); p != 0 {
		t.Fatalf("主动技能不应计入被动战力，实际 %.1f", p)
	}
	// 同一被动重复装配只算一次
	if p := PassivePowerSum([]string{"kp_bulwark", "kp_bulwark"}, nil); p != 80 {
		t.Fatalf("重复被动应去重，实际 %.1f", p)
	}

	// 关键口径：纯特效被动（无属性）→ 战力正好 +80/个
	base := CalcStats(Avatar{Level: 30, ClassID: "knight"})
	withTwo := CalcStats(Avatar{Level: 30, ClassID: "knight", Passives: []string{"kp_bulwark", "kp_thorns"}})
	if base.HP != withTwo.HP || base.ATK != withTwo.ATK {
		t.Fatalf("纯特效被动不应改变属性：base=%+v with=%+v", base, withTwo)
	}
	if withTwo.Power != base.Power+160 {
		t.Fatalf("纯特效被动战力应 +160，实际 +%.1f", withTwo.Power-base.Power)
	}
	t.Logf("固定分口径：纯特效被动 +160 ✓（基础战力 %.0f → %.0f）", base.Power, withTwo.Power)
}

// v1.9.89 属性型被动：加成生效到属性上，但战力只按固定分计（不随属性重复增长）。
func TestPassiveAttrButFixedPower(t *testing.T) {
	if err := Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	base := CalcStats(Avatar{Level: 30, ClassID: "knight"})
	withG := CalcStats(Avatar{Level: 30, ClassID: "knight", Passives: []string{"kp_guardian"}})
	// 守护誓言：生命 +15%，防御 +12%
	if math.Abs(withG.HP-base.HP*1.15) > 0.01 {
		t.Fatalf("生命应为 %.2f（+15%%），实际 %.2f", base.HP*1.15, withG.HP)
	}
	if math.Abs(withG.DEF-base.DEF*1.12) > 0.01 {
		t.Fatalf("防御应为 %.2f（+12%%），实际 %.2f", base.DEF*1.12, withG.DEF)
	}
	// 属性涨了，但战力只 +80（不按属性增量重复计分）
	if withG.Power != base.Power+80 {
		t.Fatalf("战力应只 +80（固定档位分），实际 +%.1f", withG.Power-base.Power)
	}
	// 等级提升：属性按 +8%/级 放大，战力按等级系数同步涨
	withG8 := CalcStats(Avatar{Level: 30, ClassID: "knight", Passives: []string{"kp_guardian"}, SkillLv: map[string]int{"kp_guardian": 8}})
	if withG8.HP <= withG.HP {
		t.Fatalf("被动升级后生命应提升：8 级 %.2f vs 1 级 %.2f", withG8.HP, withG.HP)
	}
	if withG8.Power != base.Power+125 {
		t.Fatalf("8 级被动战力应为 base+125，实际 +%.1f", withG8.Power-base.Power)
	}
	t.Logf("属性型被动：HP %.1f→%.1f，DEF %.1f→%.1f，战力 +80（固定分，不随属性虚高）",
		base.HP, withG.HP, base.DEF, withG.DEF)
}

// v1.9.89 负面被动（虚空之躯：攻击 +18%、生命 -10%）不能把属性压成 0 或负数。
func TestPassiveNegativeClamp(t *testing.T) {
	if err := Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	base := CalcStats(Avatar{Level: 10, ClassID: "warlock"})
	withV := CalcStats(Avatar{Level: 10, ClassID: "warlock", Passives: []string{"wp_void"}})
	if withV.HP <= 0 || withV.ATK <= 0 {
		t.Fatalf("负面被动后属性不应归零：HP=%.1f ATK=%.1f", withV.HP, withV.ATK)
	}
	if math.Abs(withV.HP-base.HP*0.9) > 0.01 {
		t.Fatalf("生命应为 %.2f（-10%%），实际 %.2f", base.HP*0.9, withV.HP)
	}
	if math.Abs(withV.ATK-base.ATK*1.18) > 0.01 {
		t.Fatalf("攻击应为 %.2f（+18%%），实际 %.2f", base.ATK*1.18, withV.ATK)
	}
	t.Logf("虚空之躯：HP %.1f→%.1f，ATK %.1f→%.1f ✓", base.HP, withV.HP, base.ATK, withV.ATK)
}

// v1.9.89 常驻减伤：同一 seed 下怪物伤害整体 ×0.88，其余行不变。
func TestPassiveDamageReduction(t *testing.T) {
	if err := Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	mk := func(passives []string) BattleReport {
		return ResolveBattle(BattleRequest{
			MonsterID: "skeleton", MonsterLv: 10, Seed: 20261189,
			BD: []string{"w_slash"}, StartHP: 9999,
			Avatar: Avatar{Level: 30, ClassID: "knight", Passives: passives},
		})
	}
	plain, guarded := mk(nil), mk([]string{"kp_bulwark"})
	// 纯特效被动不改变属性，但改变怪物伤害 → 取前 N 行均值比较
	sumN, cn := 0.0, 0
	sumG, cg := 0.0, 0
	for _, r := range plain.Rounds {
		if r.Actor == "monster" && r.Damage > 0 {
			sumN += r.Damage
			cn++
		}
	}
	for _, r := range guarded.Rounds {
		if r.Actor == "monster" && r.Damage > 0 {
			sumG += r.Damage
			cg++
		}
	}
	if cn == 0 || cg == 0 {
		t.Fatal("两场都应出现怪物伤害行")
	}
	ratio := (sumG / float64(cg)) / (sumN / float64(cn))
	if math.Abs(ratio-0.88) > 0.02 {
		t.Fatalf("壁垒减伤后怪物伤害均值比应为 0.88，实际 %.3f", ratio)
	}
	t.Logf("壁垒：怪物伤害均值 %.2f → %.2f（比 %.3f，期望 0.88）", sumN/float64(cn), sumG/float64(cg), ratio)
}

// v1.9.89 反伤：被怪物打中后对攻击者反弹 30%，产生 passiveThorns 战报行。
func TestPassiveThorns(t *testing.T) {
	if err := Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	rep := ResolveBattle(BattleRequest{
		MonsterID: "skeleton", MonsterLv: 10, Seed: 20261190,
		BD: []string{"w_slash"}, StartHP: 9999,
		Avatar: Avatar{Level: 30, ClassID: "knight", Passives: []string{"kp_thorns"}},
	})
	hit, reflect := 0, 0
	for _, r := range rep.Rounds {
		if r.Actor == "monster" && r.Damage > 0 {
			hit++
		}
		if r.Action == "passiveThorns" {
			reflect++
			if r.Damage <= 0 {
				t.Fatalf("反伤行伤害应 > 0，实际 %+v", r)
			}
		}
	}
	if hit == 0 || reflect == 0 {
		t.Fatalf("应同时出现怪物攻击与反伤行：hit=%d reflect=%d", hit, reflect)
	}
	if reflect != hit {
		t.Fatalf("每次被击中都应反伤一次：hit=%d reflect=%d", hit, reflect)
	}
	t.Logf("荆棘之甲：被击 %d 次，反伤 %d 次 ✓", hit, reflect)
}

// v1.9.89 追击：每 3 个英雄回合追加一次 passiveExtra 行，系数 0.6×。
func TestPassiveExtraAttack(t *testing.T) {
	if err := Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	rep := ResolveBattle(BattleRequest{
		MonsterID: "boss_beetle", MonsterLv: 20, Seed: 20261191,
		BD: []string{"w_slash"}, StartHP: 9999,
		Avatar: Avatar{Level: 40, ClassID: "fighter", Passives: []string{"fp_swift"}},
	})
	heroTurns, extra := 0, 0
	for _, r := range rep.Rounds {
		if r.Actor == "hero" {
			if r.Action == "passiveExtra" {
				extra++
			} else {
				heroTurns++ // 只统计真正的英雄行动（技能/普攻/召唤/hot 不算回合）
			}
		}
	}
	if extra == 0 {
		t.Fatal("疾风步应产生追击行")
	}
	// 每 3 个英雄回合一次，允许 ±1 的边界误差
	want := heroTurns / 3
	if extra < want-1 || extra > want+1 {
		t.Fatalf("追击次数应约为英雄回合数的 1/3：回合 %d，追击 %d（期望约 %d）", heroTurns, extra, want)
	}
	t.Logf("疾风步：英雄回合 %d，追击 %d 次 ✓", heroTurns, extra)
}

// v1.9.89 吸血：被动 15% 与技能自带 50% 叠加，回血量应明显高于只带技能。
func TestPassiveLifesteal(t *testing.T) {
	if err := Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	mk := func(passives []string) float64 {
		rep := ResolveBattle(BattleRequest{
			MonsterID: "boss_beetle", MonsterLv: 20, Seed: 20261192,
			BD: []string{"wl_drain"}, StartHP: 200, // 半血左右，留出回血空间
			Avatar: Avatar{Level: 40, ClassID: "warlock", Passives: passives},
		})
		heal := 0.0
		for _, r := range rep.Rounds {
			heal += r.Heal
		}
		return heal
	}
	plain := mk(nil)
	withPact := mk([]string{"wp_bloodpact"})
	if withPact <= plain {
		t.Fatalf("血契应提升回血量：无被动 %.1f，有被动 %.1f", plain, withPact)
	}
	// 吸血从 50% → 65%，同 seed 伤害序列一致，回血比应接近 1.3（封顶可能压低，故只查下限）
	if withPact < plain*1.15 {
		t.Fatalf("回血提升不明显：%.1f → %.1f", plain, withPact)
	}
	t.Logf("血契：回血 %.1f → %.1f（+%.0f%%）✓", plain, withPact, (withPact/plain-1)*100)
}

// v1.9.89 开场护盾：战斗开始即获得 25% 攻击的护盾，首个怪物攻击应被吸收。
func TestPassiveOpenShield(t *testing.T) {
	if err := Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	mk := func(passives []string) BattleReport {
		return ResolveBattle(BattleRequest{
			MonsterID: "skeleton", MonsterLv: 10, Seed: 20261193,
			BD: []string{"w_slash"}, StartHP: 9999,
			Avatar: Avatar{Level: 30, ClassID: "knight", Passives: passives},
		})
	}
	plain, shielded := mk(nil), mk([]string{"kp_aegis_master"})
	firstHit := func(rep BattleReport) float64 {
		for _, r := range rep.Rounds {
			if r.Actor == "monster" {
				return r.Damage
			}
		}
		return -1
	}
	a, b := firstHit(plain), firstHit(shielded)
	if a <= 0 || b < 0 {
		t.Fatalf("未取到首个怪物伤害：plain=%.1f shield=%.1f", a, b)
	}
	if b >= a {
		t.Fatalf("开场护盾应吸收首个攻击：无被动 %.1f，有被动 %.1f", a, b)
	}
	t.Logf("圣盾精通：首个怪物伤害 %.1f → %.1f（护盾吸收）✓", a, b)
}

// v1.9.89 周期回复：每 3 个英雄回合回 6% 最大生命。
func TestPassiveRegen(t *testing.T) {
	if err := Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	hero := CalcStats(Avatar{Level: 30, ClassID: "sage"})
	rep := ResolveBattle(BattleRequest{
		MonsterID: "boss_beetle", MonsterLv: 20, Seed: 20261194,
		BD: []string{"s_purify"}, StartHP: hero.HP * 0.4,
		Avatar: Avatar{Level: 30, ClassID: "sage", Passives: []string{"sp_sanctuary"}},
	})
	rows, maxHeal := 0, 0.0
	for _, r := range rep.Rounds {
		if r.Action == "passiveRegen" {
			rows++
			if r.Heal > maxHeal {
				maxHeal = r.Heal
			}
		}
	}
	if rows == 0 {
		t.Fatal("庇护圣所应产生被动回复行")
	}
	expect := hero.HP * 0.06
	if math.Abs(maxHeal-expect) > 0.5 {
		t.Fatalf("单次被动回复应约 %.2f（6%% 最大生命），实际 %.2f", expect, maxHeal)
	}
	t.Logf("庇护圣所：回复 %d 次，单次 %.2f（期望 %.2f）✓", rows, maxHeal, expect)
}

// v1.9.89 组队战斗：被动特效必须同样生效（队员各自的被动独立结算）。
func TestPartyPassiveEffects(t *testing.T) {
	if err := Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	// 血量调低 + 打高级 Boss：保证战斗有足够回合让怪物出手，否则一击必杀测不到反伤
	av := Avatar{Level: 15, ClassID: "knight", Passives: []string{"kp_thorns", "kp_aegis_master"}}
	rep := ResolvePartyBattle("dg_test", []WaveSpec{{Mon: "boss_beetle", Lv: 30, Count: 1}},
		[]PartyHero{{UserID: "u1", Name: "骑士", ClassID: "knight", Level: 15, Avatar: av, BD: []string{"w_slash"}}},
		20261195)
	thorns, hits := 0, 0
	for _, r := range rep.Rounds {
		if r.Action == "passiveThorns" {
			thorns++
		}
		if r.Actor == "boss" && r.Damage > 0 {
			hits++
		}
	}
	if thorns == 0 {
		t.Fatalf("组队里荆棘之甲应生效，战报共 %d 行", len(rep.Rounds))
	}
	if hits == 0 {
		t.Fatal("组队里应出现怪物伤害行")
	}
	t.Logf("组队被动：反伤 %d 次 / 怪物命中 %d 次 ✓", thorns, hits)
}
