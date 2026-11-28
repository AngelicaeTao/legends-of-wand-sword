package game

import (
	"math"
	"testing"
)

// v1.9.40 法师治疗术：一次性回血 → 持续回血（施放立即 1 跳 + 后续 hotRounds-1 跳）
// v1.9.89 数值调整：每跳 15% → 5%（施放 + 3 跳 = 4 跳，合计 20% 最大生命）
func TestHealHot(t *testing.T) {
	if err := Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	sk, ok := C().SkillByID("m_heal")
	if !ok || sk.HotRounds != 3 || sk.HotPer != 0.05 || sk.Power != 0.05 {
		t.Fatalf("m_heal 配置应为 hotRounds=3 / hotPer=0.05 / power=0.05，实际 %+v", sk)
	}
	hero := CalcStats(Avatar{Level: 10, ClassID: "mage"})
	if hero.HP <= 0 {
		t.Fatal("最大生命计算异常")
	}
	// 半血开局：缺口远大于 4 跳总回复，保证每一跳都是「满额 5%」而不是被缺口截断
	rep := ResolveBattle(BattleRequest{
		MonsterID: "slime", MonsterLv: 1, Seed: 20261126,
		StartHP: hero.HP * 0.5, BD: []string{"m_heal"},
		Avatar: Avatar{Level: 10, ClassID: "mage"},
	})
	expect := hero.HP * 0.05 // 每跳 = 5% 最大生命
	jumps, total, maxJump := 0, 0.0, 0.0
	for _, r := range rep.Rounds {
		if r.Heal > 0 {
			jumps++
			total += r.Heal
			if r.Heal > maxJump {
				maxJump = r.Heal
			}
		}
	}
	if jumps < 2 {
		t.Fatalf("持续回血应产生至少 2 次回复（施放+跳数），实际 %d 次", jumps)
	}
	// 单跳必须正好是 5% 最大生命（±0.5 容差覆盖 round2 与随机浮动之外的取整误差）
	if math.Abs(maxJump-expect) > 0.5 {
		t.Fatalf("单跳 %.2f 应为 5%% 最大生命 %.2f（±0.5）", maxJump, expect)
	}
	// 总回复上限 ≈ 4 跳 × 5%（可能因战斗提前结束少跳，不应超过 20%）
	if total > hero.HP*0.20+0.5 {
		t.Fatalf("总回复 %.1f 超过 20%% 最大生命 %.1f，疑似仍有 15%% 旧数值", total, hero.HP*0.2)
	}
	t.Logf("回复次数=%d 总回复=%.1f 单跳=%.1f 最大血量=%.1f 结果=%s（每跳 5%%，共 %.1f）",
		jumps, total, maxJump, hero.HP, rep.Result, expect)
}

// v1.9.40 战士格挡：一次性护盾 → 持续减伤（drRounds 回合内怪物伤害 × (1-drPct)）
func TestGuardDamageReduction(t *testing.T) {
	if err := Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
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
		if r.Actor == "monster" && r.DR > 0 {
			drRows++
		}
	}
	// cd=5 且战斗较长时会多次施放：每次施放标记连续 3 个减伤回合
	if drRows < 3 || drRows%3 != 0 {
		t.Fatalf("减伤回合数应为 3 的倍数（每次施放 3 回合），实际 %d", drRows)
	}
	// 减伤比例统计验证：格挡场 dr 行均值 / 无格挡场怪物行均值 ≈ 0.6
	// （两场 rng 序列错位：w_guard 效果行不消耗伤害浮动随机数，故用统计而非逐回合对比）
	sumG, sumN := 0.0, 0.0
	for _, r := range withGuard.Rounds {
		if r.Actor == "monster" && r.DR > 0 {
			sumG += r.Damage
		}
	}
	for _, r := range noGuard.Rounds {
		if r.Actor == "monster" {
			sumN += r.Damage
		}
	}
	if sumN <= 0 {
		t.Fatal("无格挡场无怪物伤害行")
	}
	ratio := (sumG / float64(drRows)) / (sumN / float64(monRowCount(noGuard)))
	if math.Abs(ratio-0.6) > 0.08 {
		t.Fatalf("减伤比例统计 %.3f 偏离 0.6（±8%% 浮动容差）", ratio)
	}
	t.Logf("格挡验证：减伤回合 %d，行均值比 %.3f（期望 0.6）", drRows, ratio)
}

// v1.9.40 怪物强度下调：base 数值不得高于旧版
func monRowCount(rep BattleReport) int {
	n := 0
	for _, r := range rep.Rounds {
		if r.Actor == "monster" {
			n++
		}
	}
	return n
}

func TestMonsterNerf(t *testing.T) {
	if err := Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	old := map[string][3]float64{
		"slime": {80, 10, 3}, "skeleton": {130, 16, 6}, "bat": {95, 20, 2},
		"boss_beetle": {680, 42, 14}, "slime_toxic": {115, 15, 4},
		"skeleton_archer": {105, 22, 4}, "bat_crimson": {125, 26, 3},
	}
	for _, m := range C().Monsters.Monsters {
		o, ok := old[m.ID]
		if !ok {
			continue
		}
		if m.Base["hp"] > o[0] || m.Base["atk"] > o[1] || m.Base["def"] > o[2] {
			t.Fatalf("%s 数值未下调: %v（旧 %v）", m.ID, m.Base, o)
		}
	}
	t.Log("7 怪 base 全部不高于旧版 ✓")
}
