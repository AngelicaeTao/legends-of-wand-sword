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
	p.Core.Material = 0
	wh0 := len(p.Warehouse)
	// net 层公式：goldBase[quality] × (1 + enhance × 0.15)；quality=2 → 300 × 1.3 = 390；材料 matBase[2]=80
	gain, matGain, err := p.WarehouseDecompose("eq_a", 390, 80)
	if err != nil { t.Fatalf("分解失败: %v", err) }
	if gain != 390 || matGain != 80 { t.Fatalf("入账应为 金币390/材料80，实际 %d/%d", gain, matGain) }
	if p.Core.Gold != 490 || p.Core.Material != 80 {
		t.Fatalf("余额异常：金币 %d（应 490），材料 %d（应 80）", p.Core.Gold, p.Core.Material)
	}
	if len(p.Warehouse) != wh0-1 { t.Fatalf("仓库应减少 1 件（%d→%d），实际 %d", wh0, wh0-1, len(p.Warehouse)) }
	if _, _, err := p.WarehouseDecompose("eq_a", 100, 10); err == nil {
		t.Fatal("重复分解应报错")
	}
	t.Logf("分解 OK：金币 +%d 材料 +%d，日志 %d 条", gain, matGain, len(p.Logs))
}

// v1.9.41 批量分解：只吃装备条目，材料/药水原样保留；收益按品质基准表累加
func TestWarehouseDecomposeAll(t *testing.T) {
	if err := game.Init("../../configs_shared"); err != nil { t.Fatal(err) }
	SetDecomposeConfig([]int{40, 120, 300, 800, 2000}, []int{10, 30, 80, 200, 500}, 0.15)
	p := NewPlayer("t_all", "分解狂", "mage")
	gold0, mat0 := p.Core.Gold, p.Core.Material // NewPlayer 自带初始金币/材料
	// 3 件装备（品质 0/1/2，强化 0/1/0）+ 2 个非装备条目
	mk := func(id string, q, enh int) Item {
		return Item{ID: id, Qty: 1, Kind: "equip",
			Equip: &Equip{ID: id, Slot: "weapon", Quality: q, Enhance: enh, Stats: map[string]float64{"atk": 9}, ClassID: "mage"}}
	}
	p.Warehouse = append(p.Warehouse,
		mk("e0", 0, 0), mk("e1", 1, 1), mk("e2", 2, 0),
		Item{ID: "potion_s", Qty: 5, Kind: "consume"},
		Item{ID: "iron", Qty: 12, Kind: "material"},
	)
	// 期望：金币 40 + 120×1.15=138 + 300 = 478；材料 10+30+80 = 120
	n, gold, mat := p.WarehouseDecomposeAll()
	if n != 3 || gold != 478 || mat != 120 {
		t.Fatalf("批量分解异常：n=%d gold=%d mat=%d（期望 3/478/120）", n, gold, mat)
	}
	if len(p.Warehouse) != 8 || p.Warehouse[6].ID != "potion_s" || p.Warehouse[7].ID != "iron" {
		t.Fatalf("非装备条目应保留（初始 6 条+追加 2 条），实际 %d 条：%v", len(p.Warehouse), p.Warehouse)
	}
	if p.Core.Gold != gold0+478 || p.Core.Material != mat0+120 {
		t.Fatalf("余额增量异常：gold +%d（应 +478）, material +%d（应 +120）", p.Core.Gold-gold0, p.Core.Material-mat0)
	}
	t.Logf("批量分解 OK：3 件 → 金币 +478 材料 +120，药水/材料保留 ✓")
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
