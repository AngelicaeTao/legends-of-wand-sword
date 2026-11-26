package game

import "testing"

// v1.9.36 群怪模版：混编组合战斗结算验证
func TestGroupBattle(t *testing.T) {
	if err := Init("../../configs_shared"); err != nil { t.Fatal(err) }
	req := BattleRequest{
		MonsterID: "skeleton", MonsterLv: 3,
		Group:  []string{"slime", "skeleton", "bat"},
		Avatar: Avatar{Level: 8, ClassID: "warrior"},
	}
	rep := ResolveBattle(req)
	if len(rep.Monsters) != 3 {
		t.Fatalf("阵容应为 3 只，实际 %d", len(rep.Monsters))
	}
	if rep.Monsters[0].Name != "史莱姆" || rep.Monsters[2].Name != "洞穴蝙蝠" {
		t.Fatalf("阵容顺序不符: %+v", rep.Monsters)
	}
	for _, r := range rep.Rounds {
		if r.MonIdx < 0 || r.MonIdx >= 3 {
			t.Fatalf("回合 %d monIdx 越界: %d", r.N, r.MonIdx)
		}
	}
	if rep.Result == "" {
		t.Fatal("无结果")
	}
	// 总血池：战报最后一回合 MonHP 应为 0（win）或 >0（lose/超时）
	last := rep.Rounds[len(rep.Rounds)-1]
	t.Logf("result=%s rounds=%d exp=%d gold=%d drops=%d 最后回合 monHp=%.0f",
		rep.Result, len(rep.Rounds), rep.Exp, rep.Gold, len(rep.Drops), last.MonHP)
	if rep.Result == "win" && last.MonHP > 0 {
		t.Fatal("胜利但血池未清零")
	}
	// 单怪路径不受影响
	single := ResolveBattle(BattleRequest{MonsterID: "slime", MonsterLv: 1, Avatar: Avatar{Level: 1, ClassID: "warrior"}})
	if len(single.Monsters) != 1 {
		t.Fatalf("单怪路径阵容应为 1，实际 %d", len(single.Monsters))
	}
	t.Logf("单怪路径 OK: %s rounds=%d", single.Result, len(single.Rounds))
}
