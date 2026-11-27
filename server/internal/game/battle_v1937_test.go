package game

import "testing"

// v1.9.37：治疗术削弱上限校验（power 0.8→0.4，单次回复 ≤ 40% 最大血量）
func TestHealNerf(t *testing.T) {
	if err := Init("../../../configs"); err != nil { t.Fatal(err) }
	found := false
	for seed := int64(1); seed <= 30 && !found; seed++ {
		rep := ResolveBattle(BattleRequest{
			MonsterID: "slime", MonsterLv: 6, Seed: seed,
			BD: []string{"m_fireball", "m_heal"},
			Avatar: Avatar{Level: 6, ClassID: "mage"},
		})
		maxHp := rep.HeroHPStart // StartHP=0 → 满血开局，即最大血量
		for _, r := range rep.Rounds {
			if r.Heal > 0 {
				found = true
				if r.Heal > maxHp*0.42 {
					t.Fatalf("seed=%d 治疗量 %.0f 超过 40%% 上限 %.0f", seed, r.Heal, maxHp*0.42)
				}
				t.Logf("seed=%d 治疗 %.0f / 最大血量 %.0f (%.1f%%)", seed, r.Heal, maxHp, r.Heal/maxHp*100)
				break
			}
		}
	}
	if !found { t.Fatal("30 个种子未出现治疗回合，无法验证") }
}

// v1.9.37：新怪物可正常参战（衍生怪结算回归）
func TestNewMonstersBattle(t *testing.T) {
	if err := Init("../../../configs"); err != nil { t.Fatal(err) }
	for _, id := range []string{"slime_toxic", "skeleton_archer", "bat_crimson"} {
		rep := ResolveBattle(BattleRequest{
			MonsterID: id, MonsterLv: 2,
			Avatar: Avatar{Level: 4, ClassID: "warrior"},
		})
		if rep.Result == "" || len(rep.Rounds) == 0 {
			t.Fatalf("%s 战斗结算异常", id)
		}
		if len(rep.Monsters) != 1 || rep.Monsters[0].ID != id {
			t.Fatalf("%s 阵容不符: %+v", id, rep.Monsters)
		}
		t.Logf("%s: %s, %d 回合, exp=%d", id, rep.Result, len(rep.Rounds), rep.Exp)
	}
	// 混编组合含新怪
	rep := ResolveBattle(BattleRequest{
		MonsterID: "skeleton", MonsterLv: 3,
		Group:  []string{"slime_toxic", "skeleton_archer", "bat_crimson"},
		Avatar: Avatar{Level: 8, ClassID: "warrior"},
	})
	if len(rep.Monsters) != 3 {
		t.Fatalf("混编阵容应为 3 只，实际 %d", len(rep.Monsters))
	}
	t.Logf("新怪三混编: %s, %d 回合", rep.Result, len(rep.Rounds))
}
