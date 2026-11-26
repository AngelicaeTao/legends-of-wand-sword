package store

import (
	"testing"

	"zjcs/server/internal/game"
)

// v1.9.42 装备升品：品质+1、属性按 mult 比例重算、混沌上限拒绝
func TestQualityUp(t *testing.T) {
	if err := game.Init("../../configs_shared"); err != nil { t.Fatal(err) }
	mults := []float64{1.0, 1.25, 1.6, 2.1, 2.8, 3.6, 4.6, 6.0}
	p := NewPlayer("t_qup", "升品师", "warrior")
	p.Equip = append(p.Equip, Equip{ID: "w_up", Slot: "weapon", Quality: 4, Enhance: 2,
		Stats: map[string]float64{"atk": 28}, ClassID: "warrior"})
	p.Core.Gold = 999999
	p.Core.Material = 999999
	// 传说(4)→彩虹(5)：mult 2.8→3.6，atk 28 → 36
	q, st, err := p.QualityUp("w_up", 5000, 700, mults)
	if err != nil { t.Fatalf("升品失败: %v", err) }
	if q != 5 { t.Fatalf("新品质应为 5(彩虹)，实际 %d", q) }
	if st["atk"] != 36 { t.Fatalf("atk 应按 mult 比例 28×3.6/2.8=36，实际 %.2f", st["atk"]) }
	// 混沌上限：连升 2 次后第 4 次应拒绝
	for i := 0; i < 2; i++ {
		if _, _, err := p.QualityUp("w_up", 5000, 700, mults); err != nil {
			t.Fatalf("第 %d 次升品不应失败: %v", i+2, err)
		}
	}
	if _, _, err := p.QualityUp("w_up", 5000, 700, mults); err == nil {
		t.Fatal("混沌(7)之上继续升品应报错")
	}
	// 资源不足拒绝
	p.Core.Gold = 0
	p.Equip[0].Quality = 0
	if _, _, err := p.QualityUp("w_up", 100, 1, mults); err == nil {
		t.Fatal("金币不足应报错")
	}
	t.Logf("升品 OK：4→5→6→7 拒绝上限 ✓，atk 28→36 ✓")
}
