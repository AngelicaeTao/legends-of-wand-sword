package game

import (
	"math"
	"testing"
)

// v1.9.87 减益技能拆成两件独立的事：power=伤害系数（0 表示纯降攻、当回合不出手），
// atkDown=命中后敌人攻击力降低比例（本场持续）。此前单个 power 一字段两义，
// 导致单人引擎把它当降攻、组队引擎把它当伤害，两端行为不一致。
func TestCurseDamageAndAtkDown(t *testing.T) {
	if err := Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	sk, ok := C().SkillByID("wl_curse")
	if !ok || sk.Power != 0.8 || sk.AtkDown != 0.25 {
		t.Fatalf("wl_curse 应配置 power=0.8 / atkDown=0.25，实际 %+v", sk)
	}
	taunt, ok := C().SkillByID("k_taunt")
	if !ok || taunt.Power != 0 || taunt.AtkDown != 0.2 {
		t.Fatalf("k_taunt 应为纯减益 power=0 / atkDown=0.2，实际 %+v", taunt)
	}

	// 木桩：高血（打得久）高攻（怪物伤害易比对）
	restoreHP := setMonsterBase("boss_beetle", "hp", 100000)
	restoreAtk := setMonsterBase("boss_beetle", "atk", 200)
	defer restoreHP()
	defer restoreAtk()

	av := Avatar{Level: 30, ClassID: "warlock"}
	hero := CalcStats(av)
	run := func(bd []string) BattleReport {
		return ResolveBattle(BattleRequest{
			MonsterID: "boss_beetle", MonsterLv: 1, Seed: 20261128,
			StartHP: hero.HP, BD: bd, Avatar: av,
		})
	}
	// 同 seed 下「带减益」与「只有普攻」两场，施放回合都只消耗一次浮动随机数，
	// 因此怪物首次攻击的 ±10% 浮动完全一致，可以直接比数值求降攻比例。
	firstMonHit := func(rep BattleReport) float64 {
		for _, r := range rep.Rounds {
			if r.Actor == "monster" {
				return r.Damage
			}
		}
		return 0
	}

	noCurse := firstMonHit(run(nil))
	if noCurse <= 0 {
		t.Fatal("对照场次没有怪物伤害，无法比对降攻")
	}

	// 1) 诅咒施放回合必须造成伤害（约 0.8×攻击，含魔防减免与 ±10% 浮动）
	curseRep := run([]string{"wl_curse"})
	var hit *BattleRound
	for i := range curseRep.Rounds {
		if curseRep.Rounds[i].Action == "wl_curse" {
			hit = &curseRep.Rounds[i]
			break
		}
	}
	if hit == nil {
		t.Fatal("整场没有施放诅咒")
	}
	lo, hi := hero.ATK*0.8*0.5, hero.ATK*0.8*1.12
	if hit.Damage < lo || hit.Damage > hi {
		t.Fatalf("诅咒伤害 %.1f 超出预期 [%.1f, %.1f]（0.8×攻击）", hit.Damage, lo, hi)
	}

	// 2) 降攻：怪物首击应降到 75%
	ratio := firstMonHit(curseRep) / noCurse
	if math.Abs(ratio-0.75) > 0.03 {
		t.Fatalf("诅咒降攻应使怪物伤害变为 75%%，实际 %.2f（对照 %.1f）", ratio, noCurse)
	}
	t.Logf("诅咒：伤害 %.1f / 怪物首击 %.1f → %.1f（%.2f 倍）", hit.Damage, noCurse, firstMonHit(curseRep), ratio)

	// 3) 嘲讽：纯降攻，施放回合 0 伤害，怪物首击应降到 80%
	tauntRep := run([]string{"k_taunt"})
	cast := 0
	for _, r := range tauntRep.Rounds {
		if r.Action != "k_taunt" {
			continue
		}
		cast++
		if r.Damage != 0 {
			t.Fatalf("嘲讽不应造成伤害，实际 %.1f", r.Damage)
		}
	}
	if cast == 0 {
		t.Fatal("整场没有施放嘲讽")
	}
	tRatio := firstMonHit(tauntRep) / noCurse
	if math.Abs(tRatio-0.8) > 0.03 {
		t.Fatalf("嘲讽降攻应使怪物伤害变为 80%%，实际 %.2f", tRatio)
	}
	t.Logf("嘲讽：施放 %d 次 0 伤害 / 怪物首击 %.1f（%.2f 倍）", cast, firstMonHit(tauntRep), tRatio)
}

// v1.9.87 组队引擎同样支持减益伤害与降攻（此前 debuff 会掉进 default 只按伤害算、没有降攻）。
func TestPartyCurseAtkDown(t *testing.T) {
	if err := Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	restoreHP := setMonsterBase("boss_beetle", "hp", 200000)
	defer restoreHP()

	av := Avatar{Level: 30, ClassID: "warlock"}
	hero := PartyHero{UserID: "u_test", Name: "术士", ClassID: "warlock", Level: 30, Avatar: av, BD: []string{"wl_curse"}}
	waves := []WaveSpec{{Mon: "boss_beetle", Lv: 1, Count: 1}}
	rep := ResolvePartyBattle("dg_test", waves, []PartyHero{hero}, 20261128)

	cast, dmg := 0, 0.0
	for _, r := range rep.Rounds {
		if r.Action == "wl_curse" {
			cast++
			dmg += r.Damage
		}
	}
	if cast == 0 {
		t.Fatal("组队场次没有施放诅咒")
	}
	if dmg <= 0 {
		t.Fatalf("组队诅咒应造成伤害，实际合计 %.1f", dmg)
	}
	t.Logf("组队诅咒：施放 %d 次，合计伤害 %.1f", cast, dmg)
}
