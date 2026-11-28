package game

import "testing"

// setMonsterBase 临时改怪物基础数值（测试专用），返回还原函数。
func setMonsterBase(id, key string, v float64) func() {
	for i := range C().Monsters.Monsters {
		m := &C().Monsters.Monsters[i]
		if m.ID == id {
			old := m.Base[key]
			m.Base[key] = v
			return func() { m.Base[key] = old }
		}
	}
	return func() {}
}

// v1.9.86 撤销 v1.9.85 的「吸血并入火球」：吸血效果回到术士「吸取」wl_drain，火球术恢复纯输出。
func TestWarlockDrainLifesteal(t *testing.T) {
	if err := Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	sk, ok := C().SkillByID("wl_drain")
	if !ok || sk.Lifesteal != 0.5 {
		t.Fatalf("wl_drain 应配置 lifesteal=0.5，实际 %+v", sk)
	}
	// 火球术不应再带吸血，否则等于吸血的术士专属效果被全职业共享
	if fb, ok := C().SkillByID("m_fireball"); !ok || fb.Lifesteal != 0 {
		t.Fatalf("m_fireball 不应有吸血，实际 %+v", fb)
	}
	// 木桩：高血低攻，保证打满多轮且不会被打死
	restoreHP := setMonsterBase("boss_beetle", "hp", 5000)
	restoreAtk := setMonsterBase("boss_beetle", "atk", 1)
	defer restoreHP()
	defer restoreAtk()

	av := Avatar{Level: 30, ClassID: "warlock"}
	hero := CalcStats(av)
	rep := ResolveBattle(BattleRequest{
		MonsterID: "boss_beetle", MonsterLv: 1, Seed: 20261128,
		StartHP: hero.HP * 0.3, BD: []string{"wl_drain"}, Avatar: av,
	})
	hits, exact := 0, 0
	for _, r := range rep.Rounds {
		if r.Action != "wl_drain" {
			continue
		}
		hits++
		if r.Damage <= 0 {
			t.Fatalf("吸取应造成伤害：%+v", r)
		}
		if r.Heal <= 0 {
			t.Fatalf("吸取命中必须回血：damage=%.1f heal=%.1f", r.Damage, r.Heal)
		}
		// 回血 = min(伤害×50%, 血量缺口)，所以永远不应超过 50%+取整误差
		if r.Heal > r.Damage*0.5+2 {
			t.Fatalf("回血超过伤害的 50%%：damage=%.1f heal=%.1f", r.Damage, r.Heal)
		}
		if r.Heal >= r.Damage*0.5-2 {
			exact++
		}
	}
	if hits == 0 {
		t.Fatalf("整场一次吸取都没放出来")
	}
	if exact == 0 {
		t.Fatalf("没有任何一次回血达到伤害的 50%%（疑似未生效）")
	}
	t.Logf("吸取命中 %d 次，其中 %d 次精确 50%%", hits, exact)
}

// v1.9.83 术士「召唤」：施放当回合不出手，之后 6 个英雄回合每回合造成 0.5× 攻击伤害（v1.9.82 为 0.2×）。
func TestWarlockSummonCompanion(t *testing.T) {
	if err := Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	sk, ok := C().SkillByID("wl_summon")
	if !ok || sk.Kind != "summon" || sk.SummonRounds != 6 || sk.SummonPower != 0.5 {
		t.Fatalf("wl_summon 应配置 kind=summon/summonRounds=6/summonPower=0.5，实际 %+v", sk)
	}
	if sk.Power != 0 {
		t.Fatalf("召唤本体不应再有直接伤害，power 应为 0，实际 %v", sk.Power)
	}
	restoreHP := setMonsterBase("boss_beetle", "hp", 100000)
	restoreAtk := setMonsterBase("boss_beetle", "atk", 1)
	defer restoreHP()
	defer restoreAtk()

	av := Avatar{Level: 30, ClassID: "warlock"}
	hero := CalcStats(av)
	run := func(skillLv map[string]int) (cast int, ticks int, total float64, first float64) {
		rep := ResolveBattle(BattleRequest{
			MonsterID: "boss_beetle", MonsterLv: 1, Seed: 20261128,
			StartHP: hero.HP, BD: []string{"wl_summon"}, Avatar: av, SkillLv: skillLv,
		})
		for _, r := range rep.Rounds {
			switch r.Action {
			case "wl_summon":
				cast++
				if r.Damage > 0 {
					t.Fatalf("召唤登场当回合不应造成伤害，实际 %.1f", r.Damage)
				}
			case "summonHit":
				ticks++
				if ticks == 1 {
					first = r.Damage
				}
				total += r.Damage
			}
		}
		return
	}
	cast, ticks, total, first := run(nil)
	if cast == 0 {
		t.Fatalf("整场没有施放召唤")
	}
	if ticks == 0 {
		t.Fatalf("召唤物一次都没协战")
	}
	// 30 秒上限内足够召 6 次以上，至少应打满一次 6 跳
	if ticks < 6 {
		t.Fatalf("召唤物协战跳数不足：%d（应为 6）", ticks)
	}
	// 单跳约 0.5×攻击（含 ±10% 浮动与怪物魔防减免）
	lo, hi := hero.ATK*0.5*0.85, hero.ATK*0.5*1.12
	if first < lo || first > hi {
		t.Fatalf("召唤单跳 %.1f 超出预期范围 [%.1f, %.1f]（0.5×攻击）", first, lo, hi)
	}
	t.Logf("召唤：施放 %d 次 / 协战 %d 跳 / 单跳 %.1f / 合计 %.1f（攻击 %.1f）", cast, ticks, first, total, hero.ATK)

	// 技能等级 6 级（×1.4）：单跳应约 1.4 倍
	_, _, _, first6 := run(map[string]int{"wl_summon": 6})
	ratio := first6 / first
	if ratio < 1.25 || ratio > 1.55 {
		t.Fatalf("6 级召唤单跳应为 1 级的约 1.4 倍，实际 %.2f（%.1f vs %.1f）", ratio, first6, first)
	}
	t.Logf("6 级召唤单跳 %.1f，为 1 级的 %.2f 倍", first6, ratio)
}
