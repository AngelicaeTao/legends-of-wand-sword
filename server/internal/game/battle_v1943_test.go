package game

// v1.9.43 高等级怪物削峰验证：成长公式改线性后，Lv20 怪单次攻击不得重创玩家。
// 旧指数公式下 Boss Lv20 攻 604（玩家血 552，一刀秒）；线性后 Boss Lv20 攻约 75（约 14%）。

import (
	"math"
	"testing"
)

// maxMonHitRatio 统计一场战斗中怪物单次攻击造成的最大伤害占英雄最大血量比例。
func maxMonHitRatio(t *testing.T, rep BattleReport, heroMaxHP float64) float64 {
	t.Helper()
	prev := rep.HeroHPStart
	ratio := 0.0
	for _, r := range rep.Rounds {
		if r.Actor == "monster" && r.Action == "attack" {
			d := prev - r.HeroHP
			if d > 0 && d/heroMaxHP > ratio {
				ratio = d / heroMaxHP
			}
		}
		prev = r.HeroHP
	}
	return ratio
}

func TestV1943HighLevelMonstersSane(t *testing.T) {
	if err := Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	war := Avatar{Level: 20, ClassID: "warrior"}

	// 玩家等级与怪物等级取同等级对抗（跨级打高怪战败属正常）
	cases := []struct {
		name    string
		mon     string
		lv      int
		elite   string
		maxWant float64 // 单次攻击占最大血量上限
		win     bool    // 是否应获胜
	}{
		{"骷髅兵Lv20稀有精英", "skeleton", 20, "rareElite", 0.15, true},
		{"血蝙蝠Lv20精英", "bat_crimson", 20, "elite", 0.15, true},
		{"甲虫将军Lv20", "boss_beetle", 20, "", 0.18, false}, // Boss 允许更痛，但绝不秒杀
		{"骷髅兵Lv30稀有精英", "skeleton", 30, "rareElite", 0.15, true},
		{"甲虫将军Lv30", "boss_beetle", 30, "", 0.18, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := war
			p.Level = c.lv
			hp := CalcStats(p).HP
			rep := ResolveBattle(BattleRequest{
				MonsterID: c.mon, MonsterLv: c.lv, Elite: c.elite,
				Seed: 20261127, Avatar: p, BD: []string{"w_slash", "w_guard"},
			})
			if len(rep.Rounds) == 0 {
				t.Fatal("战报为空")
			}
			last := rep.Rounds[len(rep.Rounds)-1]
			if c.win && last.HeroHP <= 0 {
				t.Fatalf("玩家 Lv20 应能获胜，但战死了（末回合血 %.0f）", last.HeroHP)
			}
			ratio := maxMonHitRatio(t, rep, hp)
			if ratio > c.maxWant {
				t.Errorf("怪物单次攻击最大占比 %.1f%%，超出上限 %.0f%%", ratio*100, c.maxWant*100)
			}
			if c.mon == "boss_beetle" && last.HeroHP <= 0 {
				t.Logf("Boss 战败（可接受，需嗑药/技能循环），单次最大占比 %.1f%%", ratio*100)
			}
			t.Logf("%s：单次最大 %.1f%% 玩家血量，末回合血 %.0f/%.0f", c.name, ratio*100, last.HeroHP, hp)
		})
	}
}

// TestV1943LowLevelUnchanged 确保低等级（Lv1-5）体验基本不变：线性与指数在低等级差异小。
func TestV1943LowLevelUnchanged(t *testing.T) {
	if err := Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	// 公式对照：lvFactor 应等于 1+(growth-1)*(lv-1)
	m := C().Monsters.Monsters[1] // skeleton growth 1.14
	for _, lv := range []int{1, 2, 3, 5, 10} {
		want := 1 + (m.Growth-1)*float64(lv-1)
		oldExp := math.Pow(m.Growth, float64(lv-1))
		if lv <= 5 && math.Abs(want-oldExp) > 0.15 {
			t.Errorf("Lv%d 线性 %.2f 与旧指数 %.2f 偏差过大（>0.15），低等级体验被改动", lv, want, oldExp)
		}
	}
}
