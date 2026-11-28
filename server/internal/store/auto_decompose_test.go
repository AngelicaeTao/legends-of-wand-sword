package store

import (
	"strings"
	"testing"

	"zjcs/server/internal/game"
)

// v1.9.46 背包自动分解：新装备战力不高于同部位已穿戴装备时，直接分解成金币+材料，不进背包。
func TestAutoDecomposeWorseEquip(t *testing.T) {
	if err := game.Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	p := NewPlayer("t_auto_dec", "分拣侠", "warrior")
	// 新手档：武器已穿戴（练习剑），其余在背包未穿戴
	var worn *Equip
	for i := range p.Equip {
		if p.Equip[i].Equipped && p.Equip[i].Slot == "weapon" {
			worn = &p.Equip[i]
			break
		}
	}
	if worn == nil {
		t.Fatal("初始存档应有已穿戴武器")
	}
	wornPw := game.EquipContribution(p.Core.ClassID, toGameEquip(*worn))

	weak := game.EquipItem{ID: "weak_w", Slot: "weapon", Quality: 0, ClassID: "warrior",
		Stats: map[string]float64{"atk": 1}} // 战力远低于练习剑
	strong := game.EquipItem{ID: "strong_w", Slot: "weapon", Quality: 3, ClassID: "warrior",
		Stats: map[string]float64{"atk": 999}} // 远高于练习剑

	kept, gold, mat, n, _, logs := p.autoDecomposeWorse([]game.EquipItem{weak, strong})

	if n != 1 {
		t.Fatalf("应只分解 1 件（低战力那把），实际 %d 件", n)
	}
	if len(kept) != 1 || kept[0].ID != "strong_w" {
		t.Fatalf("高战力装备应保留，实际保留 %v", kept)
	}
	if gold <= 0 || mat <= 0 {
		t.Fatalf("分解应产出金币与材料，实际 金币 %d 材料 %d", gold, mat)
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "自动分解") {
		t.Fatalf("应产生 1 条自动分解日志，实际 %v", logs)
	}
	if !strings.Contains(logs[0], "战力") {
		t.Fatalf("日志应说明战力对比，实际：%s", logs[0])
	}
	t.Logf("穿戴中战力 %.0f，分解收益 金币+%d 材料+%d，日志：%s", wornPw, gold, mat, logs[0])
}

// v1.9.46 跨系武器：角色永远穿不上，无论战力多高都直接分解。
func TestAutoDecomposeCrossClassWeapon(t *testing.T) {
	if err := game.Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	p := NewPlayer("t_auto_cross", "分拣侠", "warrior")
	mageWeapon := game.EquipItem{ID: "mage_w", Slot: "weapon", Quality: 5, ClassID: "mage",
		Stats: map[string]float64{"atk": 5000}} // 战力极高但战士用不了

	kept, gold, mat, n, _, _ := p.autoDecomposeWorse([]game.EquipItem{mageWeapon})
	if n != 1 || len(kept) != 0 {
		t.Fatalf("异系武器应全部分解，实际分解 %d 件、保留 %d 件", n, len(kept))
	}
	if gold <= 0 || mat <= 0 {
		t.Fatalf("分解应产收益，实际 金币 %d 材料 %d", gold, mat)
	}
}

// v1.9.46 没有同部位穿戴装备时，新装备一律保留（新手不会一上来就被分解光）。
func TestAutoDecomposeKeepsWhenSlotEmpty(t *testing.T) {
	if err := game.Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	p := NewPlayer("t_auto_empty", "分拣侠", "warrior")
	for i := range p.Equip { // 脱下所有装备
		p.Equip[i].Equipped = false
	}
	items := []game.EquipItem{
		{ID: "h1", Slot: "helmet", Quality: 0, Stats: map[string]float64{"hp": 10}},
		{ID: "a1", Slot: "armor", Quality: 0, Stats: map[string]float64{"hp": 10}},
	}
	kept, gold, mat, n, _, _ := p.autoDecomposeWorse(items)
	if n != 0 || len(kept) != 2 {
		t.Fatalf("空部位应全部保留，实际分解 %d 件、保留 %d 件（金币 %d 材料 %d）", n, len(kept), gold, mat)
	}
}

// v1.9.46 开箱奖励接入：低战力装备不进背包，收益并入金币/材料，并在 items 里给出分解明细。
func TestApplyChestRewardAutoDecompose(t *testing.T) {
	if err := game.Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	p := NewPlayer("t_chest_dec", "开箱侠", "warrior")
	eq0 := len(p.Equip)
	gold0, mat0 := p.Core.Gold, p.Core.Material

	weak := game.EquipItem{ID: "weak_w2", Slot: "weapon", Quality: 0, ClassID: "warrior",
		Stats: map[string]float64{"atk": 1}}
	_, items := ApplyChestReward(p, 0, 0, 0, 0, 0, []game.EquipItem{weak}, nil)

	if len(p.Equip) != eq0 {
		t.Fatalf("低战力装备不应进背包：开箱前 %d 件，开箱后 %d 件", eq0, len(p.Equip))
	}
	if p.Core.Gold <= gold0 || p.Core.Material <= mat0 {
		t.Fatalf("分解收益未入账：金币 %d→%d，材料 %d→%d", gold0, p.Core.Gold, mat0, p.Core.Material)
	}
	found := false
	for _, it := range items {
		if it["kind"] == "decompose" {
			found = true
		}
	}
	if !found {
		t.Fatalf("开箱明细应包含 decompose 条目，实际 %v", items)
	}
}
