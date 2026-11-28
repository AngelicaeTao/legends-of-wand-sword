package store

import (
	"testing"

	"zjcs/server/internal/game"
)

// v1.9.93 具名材料入包链回归：
//   - 打怪战报里的 materials 要真正落到 ext.bag（不是只回给前端看一眼）
//   - 开箱明细 items 里要出现 kind=mat 的条目，且数量与入包一致
//   - 落到背包后必须能被商店按专价回收（秘银 400 金 / 40 钻）
func TestAddNamedMaterialsToBag(t *testing.T) {
	if err := game.Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	p := NewPlayer("t_named_mat", "拾荒者", "warrior")
	// 新角色自带 秘银×2（ext.bag），先确认基线
	if n := bagQty(p, "秘银"); n != 2 {
		t.Fatalf("新角色应有秘银 2，实际 %d", n)
	}

	drops := []game.NamedMaterialDrop{
		{ID: "秘银", Name: "秘银", Qty: 1},
		{ID: "钢锭", Name: "钢锭", Qty: 3},
	}
	logs := AddNamedMaterials(p, drops)
	if len(logs) != 2 {
		t.Fatalf("应产出 2 行获得日志，实际 %d：%v", len(logs), logs)
	}
	if n := bagQty(p, "秘银"); n != 3 {
		t.Fatalf("秘银应累加为 3，实际 %d", n)
	}
	if n := bagQty(p, "钢锭"); n != 3 {
		t.Fatalf("钢锭应累加为 3，实际 %d", n)
	}

	// 第二次掉落继续累加（不能覆盖）
	AddNamedMaterials(p, drops)
	if n := bagQty(p, "秘银"); n != 4 {
		t.Fatalf("二次掉落后秘银应为 4，实际 %d", n)
	}

	// 打到手就能卖：秘银 40 钻/件（economy.shop.sell.items 专价）
	unit, got, err := p.ShopSell("秘银", 2, "gem")
	if err != nil {
		t.Fatalf("回收秘银失败：%v", err)
	}
	if unit != 40 || got != 80 {
		t.Fatalf("秘银回收价应为 40/件 共 80，实际 unit=%d got=%d", unit, got)
	}
	if n := bagQty(p, "秘银"); n != 2 {
		t.Fatalf("卖掉 2 件后应剩 2，实际 %d", n)
	}

	// 空掉落 / 非法条目不应污染背包
	if logs := AddNamedMaterials(p, nil); logs != nil {
		t.Fatalf("空掉落不应产生日志，实际 %v", logs)
	}
	AddNamedMaterials(p, []game.NamedMaterialDrop{{ID: "", Qty: 5}, {ID: "精铁", Qty: 0}})
	if n := bagQty(p, "精铁"); n != 0 {
		t.Fatalf("qty<=0 的条目不应入包，精铁实际 %d", n)
	}
}

// 战报 materials → ApplyBattleReport 必须入包；开箱 materials → items 明细 + 入包。
func TestNamedMaterialsInReportAndChest(t *testing.T) {
	if err := game.Init("../../../configs"); err != nil {
		t.Fatal(err)
	}

	// 1) 战斗结算
	p := NewPlayer("t_named_rep", "送信人", "warrior")
	rep := game.BattleReport{
		Result: "win", Exp: 1, Gold: 1, HeroHPEnd: 100,
		Materials: []game.NamedMaterialDrop{{ID: "蓝宝石", Name: "蓝宝石", Qty: 2}},
	}
	ApplyBattleReport(p, &rep)
	if n := bagQty(p, "蓝宝石"); n != 2 {
		t.Fatalf("战报材料未入包：蓝宝石应为 2，实际 %d", n)
	}

	// 2) 开箱结算
	p2 := NewPlayer("t_named_chest", "开箱人", "warrior")
	_, items := ApplyChestReward(p2, 0, 0, 0, 5, 0, nil,
		[]game.NamedMaterialDrop{{ID: "皮革", Name: "皮革", Qty: 3}})
	if n := bagQty(p2, "皮革"); n != 3 {
		t.Fatalf("开箱材料未入包：皮革应为 3，实际 %d", n)
	}
	found := false
	for _, it := range items {
		if it["kind"] == "mat" && it["id"] == "皮革" && it["qty"] == 3 {
			found = true
		}
	}
	if !found {
		t.Fatalf("开箱 items 明细缺少 kind=mat 的皮革条目：%+v", items)
	}
	// 强化材料计数器仍按老路走 kind=material，两者不能混为一谈
	if it := firstOfKind(items, "material"); it != nil {
		if it["id"] != "material" {
			t.Fatalf("强化材料条目的 id 应固定为 material，实际 %v", it["id"])
		}
	}
}

func firstOfKind(items []map[string]any, kind string) map[string]any {
	for _, it := range items {
		if it["kind"] == kind {
			return it
		}
	}
	return nil
}
