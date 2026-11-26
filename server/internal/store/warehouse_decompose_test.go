package store

import (
	"testing"

	"zjcs/server/internal/game"
)

// v1.9.40 仓库分解：装备 → 金币，条目移除；存入/取出保留武器系标（顺带修复 v1.9.38 遗漏）
func TestWarehouseDecompose(t *testing.T) {
	if err := game.Init("../../configs_shared"); err != nil { t.Fatal(err) }
	p := NewPlayer("t_dec", "分解侠", "warrior")
	p.Warehouse = append(p.Warehouse, Item{
		ID: "eq_a", Qty: 1, Kind: "equip",
		Equip: &Equip{ID: "精钢战刃", Slot: "weapon", Quality: 2, Enhance: 2,
			Stats: map[string]float64{"atk": 15}, ClassID: "warrior"},
	})
	p.Core.Gold = 100
	wh0 := len(p.Warehouse)
	// net 层公式：goldBase[quality] × (1 + enhance × 0.15)；quality=2 → 300 × 1.3 = 390
	gain, err := p.WarehouseDecompose("eq_a", 390)
	if err != nil { t.Fatalf("分解失败: %v", err) }
	if gain != 390 { t.Fatalf("入账金币应为 390，实际 %d", gain) }
	if p.Core.Gold != 490 { t.Fatalf("金币余额应为 490，实际 %d", p.Core.Gold) }
	if len(p.Warehouse) != wh0-1 { t.Fatalf("仓库应减少 1 件（%d→%d），实际 %d", wh0, wh0-1, len(p.Warehouse)) }
	if _, err := p.WarehouseDecompose("eq_a", 100); err == nil {
		t.Fatal("重复分解应报错")
	}
	t.Logf("分解 OK：金币 +%d，日志 %d 条", gain, len(p.Logs))
}

// 存入仓库再取出：武器 ClassID 必须随行（v1.9.40 修复 v1.9.38 存入丢系标）
func TestWarehouseEquipClassIdRoundTrip(t *testing.T) {
	if err := game.Init("../../configs_shared"); err != nil { t.Fatal(err) }
	p := NewPlayer("t_rt", "搬运工", "warrior")
	p.Equip = append(p.Equip, Equip{ID: "w_blade", Slot: "weapon", Quality: 1,
		Stats: map[string]float64{"atk": 11}, ClassID: "warrior"})
	eq0, wh0 := len(p.Equip), len(p.Warehouse)
	if err := p.WarehouseEquipIn("w_blade"); err != nil {
		t.Fatalf("存入失败: %v", err)
	}
	if len(p.Equip) != eq0-1 || len(p.Warehouse) != wh0+1 {
		t.Fatalf("存入后数量异常: equip %d→%d, wh %d→%d", eq0, len(p.Equip), wh0, len(p.Warehouse))
	}
	if p.Warehouse[wh0].Equip == nil || p.Warehouse[wh0].Equip.ClassID != "warrior" {
		t.Fatal("仓库条目武器系标丢失")
	}
	if err := p.WarehouseEquipOut("w_blade"); err != nil {
		t.Fatalf("取出失败: %v", err)
	}
	if p.Equip[0].ClassID != "warrior" {
		t.Fatalf("取出后系标丢失: %+v", p.Equip[0])
	}
	t.Log("存取往返系标保留 ✓")
}
