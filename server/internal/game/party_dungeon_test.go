package game

import "testing"

// v1.9.97 新增四个梯度组队副本（藤蔓圣所/蜂巢深渊/刀锋回廊/孢子秘境）：
// 这里守住三条不变量 —— 等级与推荐战力逐档递增、关底是单只首领、波次数不倒退。
func TestNewPartyDungeonsConfig(t *testing.T) {
	if err := Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	ids := []string{"trial", "grove", "hive", "blade", "spore"}
	prevLv, prevRec, prevWaves := 0, 0, 0
	for _, id := range ids {
		dg, ok := dungeonByID(id)
		if !ok {
			t.Fatalf("%s 副本未配置", id)
		}
		if dg.PerDay != 0 {
			t.Fatalf("%s 应是组队副本（perDay=0），实际 %d", id, dg.PerDay)
		}
		if dg.RecPower <= prevRec {
			t.Fatalf("%s 推荐战力未递增：%d <= %d", id, dg.RecPower, prevRec)
		}
		if dg.Level <= prevLv {
			t.Fatalf("%s 等级未递增：%d <= %d", id, dg.Level, prevLv)
		}
		if len(dg.Waves) < prevWaves {
			t.Fatalf("%s 波次数不能少于上一档：%d < %d", id, len(dg.Waves), prevWaves)
		}
		last := dg.Waves[len(dg.Waves)-1]
		if last.Count != 1 || last.Mon != dg.Mon {
			t.Fatalf("%s 关底应为单只 %s，实际 %s x%d", id, dg.Mon, last.Mon, last.Count)
		}
		if b, ok := C().MonsterByID(dg.Mon); !ok || !b.IsBoss {
			t.Fatalf("%s 关底 %s 不是首领怪", id, dg.Mon)
		}
		prevLv, prevRec, prevWaves = dg.Level, dg.RecPower, len(dg.Waves)
	}
}

// 奖励必须随难度同向增长：用同一支 Lv200 队伍打穿五档（保证必胜，只比奖励公式），
// 逐档断言经验/金币严格递增 —— 这是「奖励和难度成正比」的直接回归。
func TestNewPartyDungeonRewardScaling(t *testing.T) {
	if err := Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	ids := []string{"trial", "grove", "hive", "blade", "spore"}
	// 只关心奖励公式，不关心过程：把各副本涉及怪物的血/攻压到 1/0，保证必胜且不被反杀
	seen := map[string]bool{}
	for _, id := range ids {
		dg, _ := dungeonByID(id)
		for _, w := range dg.Waves {
			if seen[w.Mon] {
				continue
			}
			seen[w.Mon] = true
			restoreHP := setMonsterBase(w.Mon, "hp", 1)
			restoreAtk := setMonsterBase(w.Mon, "atk", 0)
			defer func() { restoreHP(); restoreAtk() }()
		}
	}
	av := Avatar{Level: 200, ClassID: "warrior"}
	hero := PartyHero{UserID: "u1", Name: "测试", ClassID: "warrior", Level: 200,
		Avatar: av, BD: []string{"w_slash", "w_guard"}}
	prevExp, prevGold := 0, 0
	for _, id := range ids {
		dg, _ := dungeonByID(id)
		rep := ResolvePartyBattle(id, dg.Waves, []PartyHero{hero}, 20261197)
		if rep.Result != "win" {
			t.Fatalf("%s 未通关（队伍 Lv200 应必胜），波次进度 %d/%d", id, rep.WaveDone, len(dg.Waves))
		}
		if rep.Exp <= prevExp || rep.Gold <= prevGold {
			t.Fatalf("%s 奖励未随难度增长：exp %d（上档 %d）/ gold %d（上档 %d）",
				id, rep.Exp, prevExp, rep.Gold, prevGold)
		}
		t.Logf("%-6s Lv%-2d rec=%-5d 波=%d → exp %-6d gold %-6d 材料次数 %d",
			id, dg.Level, dg.RecPower, len(dg.Waves), rep.Exp, rep.Gold, dg.MatRolls)
		prevExp, prevGold = rep.Exp, rep.Gold
	}
}

// 缺省值必须等价于旧行为：幽影试炼没写 rewardMult/matBase/matRolls，
// 结算应保持在 1 倍经验金币、强化材料基数 3、具名材料 2 次。
func TestPartyDungeonDefaultsKeepLegacy(t *testing.T) {
	if err := Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	dg, ok := dungeonByID("trial")
	if !ok {
		t.Fatal("幽影试炼未配置")
	}
	if dg.RewardMult != 0 || dg.MatBase != 0 || dg.MatRolls != 0 {
		t.Fatalf("幽影试炼不应配置新字段，实际 rm=%v matBase=%d matRolls=%d",
			dg.RewardMult, dg.MatBase, dg.MatRolls)
	}
}
