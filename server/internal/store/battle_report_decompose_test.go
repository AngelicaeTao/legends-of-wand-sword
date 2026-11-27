package store

import (
	"testing"

	"zjcs/server/internal/game"
)

// v1.9.54 结算界面不再展示被自动分解的装备：
// 战报里的 Drops 必须只剩"要留下的"，被分解的折成金币/材料计数回写给前端。
func TestApplyBattleReportFiltersDecomposed(t *testing.T) {
	if err := game.Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	p := NewPlayer("t_rep_dec", "结算侠", "warrior")

	weak := game.EquipItem{ID: "weak_w", Slot: "weapon", Quality: 0, ClassID: "warrior",
		Stats: map[string]float64{"atk": 1}}
	strong := game.EquipItem{ID: "strong_w", Slot: "weapon", Quality: 3, ClassID: "warrior",
		Stats: map[string]float64{"atk": 999}}

	rep := game.BattleReport{Result: "win", Exp: 10, Gold: 5, Drops: []game.EquipItem{weak, strong}}
	ApplyBattleReport(p, &rep)

	if len(rep.Drops) != 1 || rep.Drops[0].ID != "strong_w" {
		t.Fatalf("战报应只保留高战力装备，实际 %+v", rep.Drops)
	}
	if rep.DecomposedCount != 1 {
		t.Fatalf("应回写分解件数 1，实际 %d", rep.DecomposedCount)
	}
	if rep.DecomposedGold <= 0 || rep.DecomposedMat <= 0 {
		t.Fatalf("应回写分解收益，实际 金币 %d 材料 %d", rep.DecomposedGold, rep.DecomposedMat)
	}
	if p.Core.Gold < 5+rep.DecomposedGold {
		t.Fatalf("金币应入账：期望 ≥ %d，实际 %d", 5+rep.DecomposedGold, p.Core.Gold)
	}
	// 背包里不应出现被分解的那件
	for _, e := range p.Equip {
		if e.ID == "weak_w" {
			t.Fatal("被分解的装备不应进背包")
		}
	}
}
