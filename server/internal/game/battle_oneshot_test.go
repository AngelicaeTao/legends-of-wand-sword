package game

import "testing"

// v1.9.58 一击必杀判定：
// 遭遇总血量低于普攻保底伤害 → 直接判胜，Rounds 为空，OneShot=true；
// 血量高于保底伤害 → 正常进回合模拟。
func TestOneShotSkipBattle(t *testing.T) {
	if err := Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	// 中等装备的 40 级战士：普攻伤害约 200+，打 1 级史莱姆（55 血）必然秒杀
	strong := Avatar{Level: 40, ClassID: "warrior", Equip: []EquipItem{
		{ID: "w", Slot: "weapon", Quality: 3, Enhance: 3, Stats: map[string]float64{"atk": 40}},
	}}
	if d := CalcStats(strong).ATK * C().Skills.BasicAttack.Power; d < 120 {
		t.Fatalf("夹具攻击力过低（普攻约 %.0f），无法验证秒杀", d)
	}
	rep := ResolveBattle(BattleRequest{UserID: "t_oneshot", Avatar: strong, MonsterID: "slime", MonsterLv: 1})
	if !rep.OneShot {
		t.Fatalf("应判定为一击必杀，实际 oneShot=%v rounds=%d", rep.OneShot, len(rep.Rounds))
	}
	if rep.Result != "win" {
		t.Fatalf("判胜，实际 %q", rep.Result)
	}
	if len(rep.Rounds) != 0 {
		t.Fatalf("秒杀不应产生回合，实际 %d 回合", len(rep.Rounds))
	}
	if rep.Exp <= 0 || rep.Gold <= 0 {
		t.Fatalf("秒杀仍应结算奖励，实际 exp=%d gold=%d", rep.Exp, rep.Gold)
	}
	if rep.Seconds != 0 {
		t.Fatalf("秒杀不计战斗时长，实际 Seconds=%v", rep.Seconds)
	}

	// 一级裸装法师打同样的史莱姆：血量高于保底伤害，必须走正常战斗
	weak := Avatar{Level: 1, ClassID: "mage"}
	rep2 := ResolveBattle(BattleRequest{UserID: "t_oneshot2", Avatar: weak, MonsterID: "slime", MonsterLv: 1})
	if rep2.OneShot {
		t.Fatalf("一级裸装角色不该被判秒杀（rounds=%d）", len(rep2.Rounds))
	}
	if len(rep2.Rounds) == 0 {
		t.Fatal("非秒杀必须产生回合")
	}

	// 图域首领血量按挑战者缩放，远高于普攻：不触发秒杀
	rep3 := ResolveBattle(BattleRequest{UserID: "t_oneshot3", Avatar: strong, MonsterID: "map_boss", MonsterLv: 32, Elite: "boss"})
	if rep3.OneShot {
		t.Fatalf("图域首领不该被普攻秒杀（rounds=%d）", len(rep3.Rounds))
	}
	if len(rep3.Rounds) == 0 {
		t.Fatal("首领战必须产生回合")
	}
}
