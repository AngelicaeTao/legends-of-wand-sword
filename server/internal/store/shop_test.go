package store

import (
	"testing"

	"zjcs/server/internal/game"
)

// v1.9.53 商店兑换：金币/钻石 → 强化材料 / 经验，比例与扣费必须服务端权威。
// 比例取自 configs/economy.json shop 段：材料 10 金/件、1 钻/件；经验 1 金/点、1 钻 10 点。
func TestShopExchangeRates(t *testing.T) {
	if err := game.Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	p := NewPlayer("t_shop_rate", "商店侠", "warrior")
	p.Core.Level = 200 // 满级：GainExp 不再升级，方便直接校验经验累加
	p.Core.Gold, p.Core.Gem, p.Core.Material, p.Core.Exp = 10000, 1000, 0, 0

	// 1) 金币换材料 10:1
	cost, got, err := p.ShopExchange("material", "gold", 30)
	if err != nil {
		t.Fatalf("金币换材料失败：%v", err)
	}
	if cost != 300 || got != 30 {
		t.Fatalf("金币换材料 30 件应消耗 300 金币，实际 cost=%d got=%d", cost, got)
	}
	if p.Core.Gold != 9700 || p.Core.Material != 30 {
		t.Fatalf("扣费/入账错误：gold=%d material=%d", p.Core.Gold, p.Core.Material)
	}

	// 2) 钻石换材料 1:1
	cost, got, err = p.ShopExchange("material", "gem", 20)
	if err != nil {
		t.Fatalf("钻石换材料失败：%v", err)
	}
	if cost != 20 || got != 20 || p.Core.Gem != 980 || p.Core.Material != 50 {
		t.Fatalf("钻石换材料错误：cost=%d got=%d gem=%d material=%d", cost, got, p.Core.Gem, p.Core.Material)
	}

	// 3) 金币换经验 1:1
	cost, got, err = p.ShopExchange("exp", "gold", 500)
	if err != nil {
		t.Fatalf("金币换经验失败：%v", err)
	}
	if cost != 500 || got != 500 || p.Core.Exp != 500 || p.Core.Gold != 9200 {
		t.Fatalf("金币换经验错误：cost=%d got=%d exp=%d gold=%d", cost, got, p.Core.Exp, p.Core.Gold)
	}

	// 4) 钻石换经验 1:10（100 点经验 = 10 钻石）
	cost, got, err = p.ShopExchange("exp", "gem", 100)
	if err != nil {
		t.Fatalf("钻石换经验失败：%v", err)
	}
	if cost != 10 || got != 100 || p.Core.Gem != 970 || p.Core.Exp != 600 {
		t.Fatalf("钻石换经验错误：cost=%d got=%d gem=%d exp=%d", cost, got, p.Core.Gem, p.Core.Exp)
	}
}

// 边界：钻石换经验必须按 10 的倍数；余额不足要报错且不能扣钱；未知商品/货币要拒绝。
func TestShopExchangeGuards(t *testing.T) {
	if err := game.Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	p := NewPlayer("t_shop_guard", "抠门侠", "warrior")
	p.Core.Level = 200
	p.Core.Gold, p.Core.Gem, p.Core.Material = 100, 5, 0

	if _, _, err := p.ShopExchange("exp", "gem", 25); err == nil {
		t.Fatal("25 点经验不是 10 的倍数，应拒绝")
	}
	if p.Core.Gem != 5 {
		t.Fatalf("被拒绝的兑换不应扣钻石，实际 gem=%d", p.Core.Gem)
	}

	if _, _, err := p.ShopExchange("material", "gold", 11); err == nil {
		t.Fatal("100 金币只够 10 件材料，11 件应报余额不足")
	}
	if p.Core.Gold != 100 || p.Core.Material != 0 {
		t.Fatalf("余额不足不应扣费，实际 gold=%d material=%d", p.Core.Gold, p.Core.Material)
	}

	if _, _, err := p.ShopExchange("material", "gold", 10); err != nil {
		t.Fatalf("刚好够 10 件应成功：%v", err)
	}
	if p.Core.Gold != 0 || p.Core.Material != 10 {
		t.Fatalf("成功兑换后 gold=%d material=%d，期望 0/10", p.Core.Gold, p.Core.Material)
	}

	if _, _, err := p.ShopExchange("gold", "gold", 1); err == nil {
		t.Fatal("未知商品应拒绝")
	}
	if _, _, err := p.ShopExchange("material", "diamond", 1); err == nil {
		t.Fatal("未知货币应拒绝")
	}
	if _, _, err := p.ShopExchange("material", "gem", 0); err == nil {
		t.Fatal("数量 0 应拒绝")
	}
}
