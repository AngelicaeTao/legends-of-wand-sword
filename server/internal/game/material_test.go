package game

import (
	"math/rand"
	"testing"
)

// v1.9.93 具名材料掉落链回归。
//
// 覆盖四件事：
//  1. 权重表能正确解析（6 种材料、权重降序即稀有度升序、qty 区间落到配置值）
//  2. 三个来源的触发概率与 roll 次数按配置生效（首领必掉、普通怪约 8%）
//  3. 同一次 roll 里同种材料会合并，不会出现两条「秘银」
//  4. 配置整段缺失时返回 nil，不 panic（老配置/裁剪配置也能启动）
func TestNamedMaterialTable(t *testing.T) {
	if err := Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	rows := namedMatTable()
	if len(rows) != 6 {
		t.Fatalf("掉落表应有 6 种具名材料，实际 %d：%+v", len(rows), rows)
	}
	// 排序来自 weight 升序：秘银(5) 最稀有，必须排第一
	if rows[0].id != "秘银" {
		t.Fatalf("权重升序后首位应是秘银，实际 %s（weight=%d）", rows[0].id, rows[0].weight)
	}
	if last := rows[len(rows)-1]; last.id != "布料" {
		t.Fatalf("权重升序后末位应是布料，实际 %s", last.id)
	}
	want := map[string][2]int{
		"布料": {1, 3}, "皮革": {1, 3}, "钢锭": {1, 2},
		"精铁": {1, 2}, "蓝宝石": {1, 1}, "秘银": {1, 1},
	}
	for _, r := range rows {
		w := want[r.id]
		if r.lo != w[0] || r.hi != w[1] {
			t.Fatalf("%s 数量区间错误：%d~%d，期望 %d~%d", r.id, r.lo, r.hi, w[0], w[1])
		}
	}
}

// 首领必掉、普通怪约 8%：用 20 万次采样验证概率，容忍 1 个百分点误差。
func TestRollMonsterMaterialsChance(t *testing.T) {
	if err := Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	const N = 200000
	rng := rand.New(rand.NewSource(20261128))

	normalHit := 0
	for i := 0; i < N; i++ {
		if len(RollMonsterMaterials("", false, rng)) > 0 {
			normalHit++
		}
	}
	if got := float64(normalHit) / N * 100; got < 7.0 || got > 9.0 {
		t.Fatalf("普通怪具名材料掉率应约 8%%，实测 %.2f%%", got)
	}

	for i := 0; i < 200; i++ {
		mats := RollMonsterMaterials("boss", true, rng)
		if len(mats) == 0 {
			t.Fatal("首领必须 100% 掉具名材料")
		}
	}
}

// 首领一次给 2 份（rollsBoss=2），普通怪只给 1 份；且同种材料要合并成一条。
func TestRollMonsterMaterialsRollsAndMerge(t *testing.T) {
	if err := Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 500; i++ {
		mats := RollMonsterMaterials("", false, rng)
		if len(mats) > 1 {
			t.Fatalf("普通怪 1 次 roll 最多出 1 种材料，实际 %d：%+v", len(mats), mats)
		}
		for _, m := range mats {
			if m.ID == "" || m.Name == "" || m.Qty < 1 {
				t.Fatalf("掉落条目字段不完整：%+v", m)
			}
		}
	}
	// 首领 2 次 roll：要么 1 条（同种合并/两次同种），要么 2 条，不会出现重复 id
	for i := 0; i < 200; i++ {
		mats := RollMonsterMaterials("boss", false, rng)
		if len(mats) > 2 {
			t.Fatalf("首领 2 次 roll 最多 2 种材料，实际 %d：%+v", len(mats), mats)
		}
		seen := map[string]bool{}
		for _, m := range mats {
			if seen[m.ID] {
				t.Fatalf("同种材料未合并：%+v", mats)
			}
			seen[m.ID] = true
		}
	}
}

// 开箱：水晶箱（90%/2 次）显著高于木箱（12%/1 次）。
func TestRollChestMaterialsByTier(t *testing.T) {
	if err := Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	const N = 50000
	rng := rand.New(rand.NewSource(11))
	count := func(tier string) float64 {
		hit := 0
		for i := 0; i < N; i++ {
			if len(RollChestMaterials(tier, rng)) > 0 {
				hit++
			}
		}
		return float64(hit) / N * 100
	}
	wood, crystal := count("wood"), count("crystal")
	if wood < 10 || wood > 14 {
		t.Fatalf("木箱具名材料概率应约 12%%，实测 %.2f%%", wood)
	}
	if crystal < 87 || crystal > 93 {
		t.Fatalf("水晶箱具名材料概率应约 90%%，实测 %.2f%%", crystal)
	}
	if crystal <= wood {
		t.Fatalf("高档次箱子必须比木箱更容易出材料：木 %.2f%% / 水晶 %.2f%%", wood, crystal)
	}
	// 未知档位按木箱兜底，不能 panic 也不能必掉
	if got := count("unknown_tier"); got < 10 || got > 14 {
		t.Fatalf("未知档位应按木箱（12%%）兜底，实测 %.2f%%", got)
	}
}

// 公会材料包必给 2 份。
func TestRollGuildPackMaterials(t *testing.T) {
	if err := Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(3))
	for i := 0; i < 100; i++ {
		mats := RollGuildPackMaterials(rng)
		if len(mats) == 0 {
			t.Fatal("公会材料包必须给具名材料")
		}
		total := 0
		for _, m := range mats {
			total += m.Qty
		}
		if total < 2 {
			t.Fatalf("公会材料包应给 2 份（可合并为 qty=2），实际 %+v", mats)
		}
	}
}

// 配置整段缺失：四个入口都返回 nil，不能 panic（老存档/精简单元测试场景）。
func TestNamedMaterialsWithoutConfig(t *testing.T) {
	if err := Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	root := C().Economy.Drop
	prev, had := root["namedMaterial"]
	delete(root, "namedMaterial")
	defer func() {
		if had {
			root["namedMaterial"] = prev
		}
	}()

	rng := rand.New(rand.NewSource(5))
	if len(RollMonsterMaterials("boss", true, rng)) != 0 {
		t.Fatal("无配置时打怪不应掉材料")
	}
	if len(RollChestMaterials("crystal", rng)) != 0 {
		t.Fatal("无配置时开箱不应掉材料")
	}
	if len(RollGuildPackMaterials(rng)) != 0 {
		t.Fatal("无配置时公会材料包不应掉材料")
	}
	if len(RollPartyDungeonMaterials(rng)) != 0 {
		t.Fatal("无配置时组队副本不应掉材料")
	}
	if len(RollChestMaterials("wood", nil)) != 0 {
		t.Fatal("rng 为 nil 时应安全返回空")
	}
}
