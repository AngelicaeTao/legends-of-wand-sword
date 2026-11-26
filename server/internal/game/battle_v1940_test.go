package game

import (
	"math"
	"testing"
)

// v1.9.40 法师治疗术：一次性回血 → 持续回血（施放立即 1 跳 + 后续 hotRounds-1 跳）
func TestHealHot(t *testing.T) {
	if err := Init("../../configs_shared"); err != nil { t.Fatal(err) }
	sk, ok := C().SkillByID("m_heal")
	if !ok || sk.HotRounds != 3 || sk.HotPer != 0.15 {
		t.Fatalf("m_heal 配置应为 hotRounds=3/hotPer=0.15，实际 %+v", sk)
	}
	// 低血开局，保证每跳不被上限截断（缺口远大于总回复）
	lowHP := 30.0
	hero := CalcStats(Avatar{Level: 10, ClassID: "mage"})
	rep := ResolveBattle(BattleRequest{
		MonsterID: "slime", MonsterLv: 1, Seed: 20261126,
		StartHP: lowHP, BD: []string{"m_heal"},
		Avatar: Avatar{Level: 10, ClassID: "mage"},
	})
	_ = hero
	jumps := 0
	total := 0.0
	maxJump := 0.0
	for _, r := range rep.Rounds {
		if r.Heal > 0 {
			jumps++
			total += r.Heal
			if r.Heal > maxJump { maxJump = r.Heal }
		}
	}
	if jumps < 2 {
		t.Fatalf("持续回血应产生至少 2 次回复（施放+跳数），实际 %d 次", jumps)
	}
	expect := lowHP * 0.15 // 每跳 = 15% 最大血量（CalcStats level10 mage 最大血量以实测为准）
	_ = expect
	// 单跳不应超过一次性旧版的 40% 上限（防回退为一次大口回血）
	if maxJump > hero.HP*0.2+1 {
		t.Fatalf("单跳 %.1f 超过 20%% 最大血量 %.1f，疑似回退为一次性回血", maxJump, hero.HP*0.2)
	}
	// 总回复 = 3 跳 × 15% = 45% 最大血量（若开局血量不足 45% 缺口则可能被截断；缺口 30 血足够小）
	t.Logf("回复次数=%d 总回复=%.1f 单跳=%.1f 最大血量=%.1f 结果=%s", jumps, total, maxJump, hero.HP, rep.Result)
	if total < hero.HP*0.40 {
		t.Fatalf("总回复 %.1f 低于 40%% 最大血量（3 跳应约 45%%）", total)
	}
	if total > hero.HP*0.50 {
		t.Fatalf("总回复 %.1f 超过 50%% 最大血量，数值过强", total)
	}
}

// v1.9.40 战士格挡：一次性护盾 → 持续减伤（drRounds 回合内怪物伤害 × (1-drPct)）
func TestGuardDamageReduction(t *testing.T) {
	if err := Init("../../configs_shared"); err != nil { t.Fatal(err) }
	sk, ok := C().SkillByID("w_guard")
	if !ok || sk.DRRounds != 3 || sk.DRPct != 0.4 {
		t.Fatalf("w_guard 配置应为 drRounds=3/drPct=0.4，实际 %+v", sk)
	}
	// 同 seed 对比：带格挡 vs 不带（普攻+格挡 → 普攻×2 的对照不便，直接两场对比同怪物伤害）
	noGuard := ResolveBattle(BattleRequest{
		MonsterID: "skeleton", MonsterLv: 5, Seed: 777,
		BD: []string{"w_slash"}, Avatar: Avatar{Level: 10, ClassID: "warrior"},
	})
	withGuard := ResolveBattle(BattleRequest{
		MonsterID: "skeleton", MonsterLv: 5, Seed: 777,
		BD: []string{"w_guard"}, Avatar: Avatar{Level: 10, ClassID: "warrior"},
	})
	// 格挡场怪物伤害行应带 dr 标记
	drRows := 0
	for _, r := range withGuard.Rounds {
		if r.Actor == "monster" && r.DR > 0 { drRows++ }
	}
	// cd=5 且战斗较长时会多次施放：每次施放标记连续 3 个减伤回合
	if drRows < 3 || drRows%3 != 0 {
		t.Fatalf("减伤回合数应为 3 的倍数（每次施放 3 回合），实际 %d", drRows)
	}
	// 减伤比例统计验证：格挡场 dr 行均值 / 无格挡场怪物行均值 ≈ 0.6
	// （两场 rng 序列错位：w_guard 效果行不消耗伤害浮动随机数，故用统计而非逐回合对比）
	sumG, sumN := 0.0, 0.0
	for _, r := range withGuard.Rounds { if r.Actor == "monster" && r.DR > 0 { sumG += r.Damage } }
	for _, r := range noGuard.Rounds { if r.Actor == "monster" { sumN += r.Damage } }
	if sumN <= 0 { t.Fatal("无格挡场无怪物伤害行") }
	ratio := (sumG / float64(drRows)) / (sumN / float64(monRowCount(noGuard)))
	if math.Abs(ratio-0.6) > 0.08 {
		t.Fatalf("减伤比例统计 %.3f 偏离 0.6（±8%% 浮动容差）", ratio)
	}
	t.Logf("格挡验证：减伤回合 %d，行均值比 %.3f（期望 0.6）", drRows, ratio)
}

// v1.9.40 怪物强度下调：base 数值不得高于旧版
func monRowCount(rep BattleReport) int {
	n := 0
	for _, r := range rep.Rounds { if r.Actor == "monster" { n++ } }
	return n
}

func TestMonsterNerf(t *testing.T) {
	if err := Init("../../configs_shared"); err != nil { t.Fatal(err) }
	old := map[string][3]float64{
		"slime": {80, 10, 3}, "skeleton": {130, 16, 6}, "bat": {95, 20, 2},
		"boss_beetle": {680, 42, 14}, "slime_toxic": {115, 15, 4},
		"skeleton_archer": {105, 22, 4}, "bat_crimson": {125, 26, 3},
	}
	for _, m := range C().Monsters.Monsters {
		o, ok := old[m.ID]
		if !ok { continue }
		if m.Base["hp"] > o[0] || m.Base["atk"] > o[1] || m.Base["def"] > o[2] {
			t.Fatalf("%s 数值未下调: %v（旧 %v）", m.ID, m.Base, o)
		}
	}
	t.Log("7 怪 base 全部不高于旧版 ✓")
}
