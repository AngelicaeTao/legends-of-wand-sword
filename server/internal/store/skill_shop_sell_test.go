package store

import (
	"testing"

	"zjcs/server/internal/game"
)

// v1.9.56 技能升级：每级效果 +8%，消耗钻石随等级递增，服务端权威扣费。
func TestSkillUpgrade(t *testing.T) {
	if err := game.Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	p := NewPlayer("t_skill_up", "技能侠", "warrior")
	p.Core.Gem = 5000

	// 战士初始技能：重斩 w_slash（初始即解锁）
	base := p.SkillLevelOf("w_slash")
	if base != 1 {
		t.Fatalf("新角色技能应为 1 级，实际 %d", base)
	}
	if m := game.SkillEffectMult(1); m != 1 {
		t.Fatalf("1 级倍率应为 1.0，实际 %v", m)
	}

	// 1→2 级：消耗 baseGem（配置 60）
	cost1 := game.SkillUpgradeCost(1)
	lv, cost, err := p.SkillUpgrade("w_slash")
	if err != nil {
		t.Fatalf("升级失败：%v", err)
	}
	if lv != 2 || cost != cost1 {
		t.Fatalf("应升到 2 级并扣 %d 钻，实际 lv=%d cost=%d", cost1, lv, cost)
	}
	if p.Core.Gem != 5000-cost1 {
		t.Fatalf("钻石扣费错误：%d", p.Core.Gem)
	}
	// 2 级效果 = 1.08
	if m := game.SkillEffectMult(2); m < 1.0799 || m > 1.0801 {
		t.Fatalf("2 级倍率应为 1.08，实际 %v", m)
	}

	// 2→3 级递增：应比 1→2 贵
	if c := game.SkillUpgradeCost(2); c <= cost1 {
		t.Fatalf("升级消耗应随等级递增：2→3=%d, 1→2=%d", c, cost1)
	}

	// 不在当前职业技能池的技能不能升（骑士系圣盾，战士池里没有）
	if _, _, err := p.SkillUpgrade("k_aegis"); err == nil {
		t.Fatalf("非本职业技能不应允许升级")
	}
	// 跨职业技能不能升
	if _, _, err := p.SkillUpgrade("m_fireball"); err == nil {
		t.Fatalf("跨职业技能不应允许升级")
	}
	// 未知技能
	if _, _, err := p.SkillUpgrade("nope"); err == nil {
		t.Fatalf("未知技能应报错")
	}

	// 钻石不足：不扣费、不升级
	p.Core.Gem = 1
	lvBefore := p.SkillLevelOf("w_slash")
	if _, _, err := p.SkillUpgrade("w_slash"); err == nil {
		t.Fatalf("钻石不足应报错")
	}
	if p.SkillLevelOf("w_slash") != lvBefore || p.Core.Gem != 1 {
		t.Fatalf("钻石不足时不应变更等级或扣费：lv=%d gem=%d", p.SkillLevelOf("w_slash"), p.Core.Gem)
	}
}

// 满级封顶：到 maxLevel 后拒绝继续升，且不再报价。
func TestSkillUpgradeMaxLevel(t *testing.T) {
	if err := game.Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	p := NewPlayer("t_skill_max", "满级侠", "warrior")
	p.Core.Gem = 1 << 30
	max := game.SkillMaxLevel()
	p.Skills.Levels = map[string]int{"w_slash": max}
	if _, _, err := p.SkillUpgrade("w_slash"); err == nil {
		t.Fatalf("满级后应拒绝升级")
	}
	if c := game.SkillUpgradeCost(max); c != 0 {
		t.Fatalf("满级升级报价应为 0，实际 %d", c)
	}
	if m := game.SkillEffectMult(max); m < 2.4 || m > 2.6 { // 1 + 0.08*19 = 2.52
		t.Fatalf("满级倍率应约 2.52，实际 %v", m)
	}
}

// v1.9.56 背包回收：材料/药水 → 金币或钻石，按件计价，扣减正确。
// v1.9.57 单价整体 ×10，并支持初始背包（ext.bag）里的 秘银 / 金币袋。
// v1.9.68 强化材料计数器兑回货币：1 件 = 10 金币（1:10）/ 1 钻石（1:1），
// 仓库可回收物品的专价保持 v1.9.57 不变（强化材料计数器与仓库物品是两套东西）。
func TestShopSell(t *testing.T) {
	if err := game.Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	p := NewPlayer("t_shop_sell", "回收侠", "warrior")
	p.Core.Gold, p.Core.Gem = 0, 0
	p.Warehouse = []Item{
		{ID: "钢锭", Qty: 10, Kind: "材料"},
		{ID: "potion_s", Qty: 4, Kind: "consume"},
		{ID: "钢锭", Qty: 5, Kind: "材料"}, // 同 id 多堆叠
	}

	// 钢锭 120 金币/件（v1.9.57 专价不变；仓库可回收物品与强化材料计数器是两套东西），15 件合计
	unit, got, err := p.ShopSell("钢锭", 15, "gold")
	if err != nil {
		t.Fatalf("回收钢锭失败：%v", err)
	}
	if unit != 120 || got != 1800 {
		t.Fatalf("钢锭应 120 金/件、15 件共 1800，实际 unit=%d got=%d", unit, got)
	}
	if p.Core.Gold != 1800 {
		t.Fatalf("金币到账错误：%d", p.Core.Gold)
	}
	for _, it := range p.Warehouse {
		if it.ID == "钢锭" {
			t.Fatalf("钢锭应已全部移除，仍有 qty=%d", it.Qty)
		}
	}

	// 药水回收成钻石：potion_s 10 钻/件（原 1 ×10）
	unit, got, err = p.ShopSell("potion_s", 2, "gem")
	if err != nil {
		t.Fatalf("回收药水失败：%v", err)
	}
	if unit != 10 || got != 20 || p.Core.Gem != 20 {
		t.Fatalf("药水回收钻石错误：unit=%d got=%d gem=%d", unit, got, p.Core.Gem)
	}
	// 剩余 2 瓶
	qty := 0
	for _, it := range p.Warehouse {
		if it.ID == "potion_s" { qty += it.Qty }
	}
	if qty != 2 {
		t.Fatalf("药水应剩 2 瓶，实际 %d", qty)
	}

	// 数量不足：报错且不扣
	if _, _, err := p.ShopSell("potion_s", 99, "gold"); err == nil {
		t.Fatalf("超出持有量应报错")
	}
	// 装备不回收
	p.Warehouse = append(p.Warehouse, Item{ID: "铁剑", Qty: 1, Kind: "equip"})
	if _, _, err := p.ShopSell("铁剑", 1, "gold"); err == nil {
		t.Fatalf("装备不应允许回收")
	}
	// 非法货币 / 负数 / 0
	if _, _, err := p.ShopSell("potion_s", 1, "exp"); err == nil {
		t.Fatalf("非法货币应报错")
	}
	if _, _, err := p.ShopSell("potion_s", 0, "gold"); err == nil {
		t.Fatalf("数量 0 应报错")
	}

	// ---- v1.9.57 初始背包（ext.bag）回收：新角色自带 秘银×2、金币袋×5 ----
	if n := bagQty(p, "秘银"); n != 2 {
		t.Fatalf("新角色应有秘银 2，实际 %d", n)
	}
	unit, got, err = p.ShopSell("秘银", 2, "gem") // 40 钻/件
	if err != nil {
		t.Fatalf("回收秘银失败：%v", err)
	}
	if unit != 40 || got != 80 || p.Core.Gem != 100 {
		t.Fatalf("秘银回收错误：unit=%d got=%d gem=%d", unit, got, p.Core.Gem)
	}
	if n := bagQty(p, "秘银"); n != 0 {
		t.Fatalf("秘银应已全部卖空，背包仍有 %d", n)
	}
	if bag, _ := p.Ext["bag"].(map[string]any); bag != nil {
		if _, ok := bag["秘银"]; ok {
			t.Fatalf("秘银卖空后应从 ext.bag 删除键（背包面板不再显示 0）")
		}
	}

	// 仓库 + 背包混合：仓库 1 个金币袋，背包 5 个，卖 3 → 先扣仓库再扣背包
	p.Warehouse = append(p.Warehouse, Item{ID: "金币袋", Qty: 1, Kind: "材料"})
	unit, got, err = p.ShopSell("金币袋", 3, "gold") // 500 金/件
	if err != nil {
		t.Fatalf("回收金币袋失败：%v", err)
	}
	if unit != 500 || got != 1500 {
		t.Fatalf("金币袋应 500 金/件、3 件共 1500，实际 unit=%d got=%d", unit, got)
	}
	if n := bagQty(p, "金币袋"); n != 3 { // 5 - (3-1) = 3
		t.Fatalf("金币袋背包应剩 3，实际 %d", n)
	}
	for _, it := range p.Warehouse {
		if it.ID == "金币袋" {
			t.Fatalf("仓库金币袋应已扣完，仍有 qty=%d", it.Qty)
		}
	}
	// 背包合计不足：报错且一件不扣
	if _, _, err := p.ShopSell("金币袋", 99, "gold"); err == nil {
		t.Fatalf("背包数量不足应报错")
	}
	if n := bagQty(p, "金币袋"); n != 3 {
		t.Fatalf("报错后不应扣减，背包应仍为 3，实际 %d", n)
	}

	// ---- v1.9.68 强化材料计数器兑回货币：1 件 = 10 金币（1:10）/ 1 钻石（1:1） ----
	p.Core.Material = 30 // 强化材料计数器（与仓库物品无关）
	p.Core.Gold, p.Core.Gem = 0, 0

	// 兑 10 件 → 金币 100（1:10）
	cost, got2, err := p.ShopExchange("materialToGold", "material", 10)
	if err != nil {
		t.Fatalf("强化材料兑金币失败：%v", err)
	}
	if cost != 10 || got2 != 100 || p.Core.Gold != 100 || p.Core.Material != 20 {
		t.Fatalf("兑金币应 1:10：cost=%d got=%d gold=%d material=%d", cost, got2, p.Core.Gold, p.Core.Material)
	}

	// 兑 5 件 → 钻石 5（1:1）
	cost, got2, err = p.ShopExchange("materialToGem", "material", 5)
	if err != nil {
		t.Fatalf("强化材料兑钻石失败：%v", err)
	}
	if cost != 5 || got2 != 5 || p.Core.Gem != 5 || p.Core.Material != 15 {
		t.Fatalf("兑钻石应 1:1：cost=%d got=%d gem=%d material=%d", cost, got2, p.Core.Gem, p.Core.Material)
	}

	// 比例可配：改配置后再兑，金币单价生效
	// 注意：Economy.Shop 是全局配置，改完必须还原，否则会污染同包后续用例
	//（曾出现：本用例把 goldPerUnit 改成 25 后没恢复，导致后面的专价用例读到 25）。
	if cfg := game.C().Economy.Shop; cfg != nil {
		prev, had := cfg["materialOut"]
		cfg["materialOut"] = map[string]any{"goldPerUnit": float64(25), "gemPerUnit": float64(2)}
		defer func() {
			if had {
				cfg["materialOut"] = prev
			} else {
				delete(cfg, "materialOut")
			}
		}()
	}
	cost, got2, err = p.ShopExchange("materialToGold", "material", 2)
	if err != nil || got2 != 50 {
		t.Fatalf("配置 goldPerUnit=25 后兑 2 件应得 50 金：err=%v got=%d", err, got2)
	}
	cost, got2, err = p.ShopExchange("materialToGem", "material", 3)
	if err != nil || got2 != 6 || p.Core.Gem != 11 {
		t.Fatalf("配置 gemPerUnit=2 后兑 3 件应得 6 钻：err=%v got=%d gem=%d", err, got2, p.Core.Gem)
	}

	// 强化材料不足：报错且不扣
	if _, _, err := p.ShopExchange("materialToGold", "material", 999); err == nil {
		t.Fatalf("强化材料不足应报错")
	}
	// pay 不是 material：报错
	if _, _, err := p.ShopExchange("materialToGold", "gold", 1); err == nil {
		t.Fatalf("兑回卡必须消耗强化材料，用金币支付应报错")
	}
}

// v1.9.84 仓库物品专价回归：v1.9.68 把 5 种强化材料的专价行删掉、改用 default.material
// 的 1:10 之后，钢铁/皮革这些从仓库回收的价格掉到了原来的 1/12（钢锭 120→10）。
// 判定是「两套体系被混成一套」：materialOut（强化材料计数器兑回，1:10/1:1）是 v1.9.68
// 新加的，仓库物品的 items 专价属于 v1.9.57 的旧标准，两者不该互相覆盖。
// 这里把专价逐项锁住，防止下次再被当成「重复定价」删掉。
func TestShopSellMaterialPrices(t *testing.T) {
	if err := game.Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	p := NewPlayer("t_shop_mat_price", "材料价侠", "warrior")

	// v1.9.57 专价：id → {金币, 钻石}
	want := []struct {
		id, kind  string
		gold, gem int
	}{
		{"钢锭", "材料", 120, 10},
		{"精铁", "材料", 200, 20},
		{"蓝宝石", "材料", 600, 50},
		{"皮革", "材料", 80, 10},
		{"布料", "材料", 60, 10},
		{"potion_s", "consume", 150, 10},
		{"potion_l", "consume", 450, 30},
		{"秘银", "材料", 400, 40},
		{"金币袋", "材料", 500, 50},
	}
	for _, w := range want {
		p.Warehouse = []Item{{ID: w.id, Qty: 4, Kind: w.kind}}
		p.Core.Gold, p.Core.Gem = 0, 0

		unit, got, err := p.ShopSell(w.id, 3, "gold")
		if err != nil {
			t.Fatalf("%s 回收金币失败：%v", w.id, err)
		}
		if unit != w.gold || got != w.gold*3 {
			t.Fatalf("%s 金币单价应为 %d、3 件共 %d，实际 unit=%d got=%d",
				w.id, w.gold, w.gold*3, unit, got)
		}

		p.Warehouse = []Item{{ID: w.id, Qty: 4, Kind: w.kind}}
		unit, got, err = p.ShopSell(w.id, 3, "gem")
		if err != nil {
			t.Fatalf("%s 回收钻石失败：%v", w.id, err)
		}
		if unit != w.gem || got != w.gem*3 {
			t.Fatalf("%s 钻石单价应为 %d、3 件共 %d，实际 unit=%d got=%d",
				w.id, w.gem, w.gem*3, unit, got)
		}
	}

	// 未定价的材料走 default.material（100 金 / 10 钻），不是 1:10
	p.Warehouse = []Item{{ID: "月光石", Qty: 5, Kind: "材料"}}
	p.Core.Gold, p.Core.Gem = 0, 0
	unit, got, err := p.ShopSell("月光石", 2, "gold")
	if err != nil {
		t.Fatalf("未定价材料回收失败：%v", err)
	}
	if unit != 100 || got != 200 {
		t.Fatalf("未定价材料应走 default.material 100 金/件，实际 unit=%d got=%d", unit, got)
	}

	// 同一时刻，强化材料计数器兑回仍是 1 件 = 10 金币（materialOut），与上面的仓库专价互不影响
	p.Core.Material = 10
	p.Core.Gold = 0
	if _, got, err := p.ShopExchange("materialToGold", "material", 5); err != nil || got != 50 {
		t.Fatalf("强化材料计数器应仍按 1:10 兑回（5 件=50 金）：err=%v got=%d", err, got)
	}
}
