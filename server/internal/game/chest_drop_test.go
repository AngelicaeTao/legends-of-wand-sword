package game

import (
	"math/rand"
	"sort"
	"testing"
)

// v1.9.46 三档掉落权重（economy.drop.monsterChest）：
//   - 每档内部按 木>银>金>钻石>水晶 严格递减，且每种品质概率都 > 0（不能出现必不出）
//   - 怪物档次越高，高品质（钻石+水晶）概率越高：普通 9% < 精英 16% < 首领 24%
//
// v1.9.57 保底档 floor 生效后，低于保底档的权重并入保底档：
//   - 普通 floor=wood   → 木 50 / 银 28 / 金 13 / 钻 6 / 水晶 3（原样）
//   - 精英 floor=silver → 木 40 并入银 → 银 67 / 金 17 / 钻 10 / 水晶 6
//   - 首领 floor=gold   → 木 30 + 银 26 并入金 → 金 76 / 钻 14 / 水晶 10
func TestDropChestTierWeights(t *testing.T) {
	if err := Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	const N = 200000
	cases := []struct {
		kind string
		want map[int]float64 // tier -> 期望占比（%）
	}{
		{"normal", map[int]float64{1: 50, 2: 28, 3: 13, 4: 6, 5: 3}},
		{"elite", map[int]float64{2: 67, 3: 17, 4: 10, 5: 6}},
		{"boss", map[int]float64{3: 76, 4: 14, 5: 10}},
	}
	const tol = 1.2 // 允许的绝对误差（百分点），20 万次采样下 3σ 约 0.5

	highQuality := make([]float64, 0, len(cases))
	for _, c := range cases {
		sm := &SpawnManager{rand: rand.New(rand.NewSource(20251126))}
		hit := map[int]int{}
		for i := 0; i < N; i++ {
			hit[sm.rollDropTier(c.kind)]++
		}
		for tier, want := range c.want {
			got := float64(hit[tier]) / N * 100
			if diff := got - want; diff > tol || diff < -tol {
				t.Fatalf("%s tier=%d 实测 %.2f%% 偏离期望 %.0f%%（容差 %.1f）", c.kind, tier, got, want, tol)
			}
			if hit[tier] == 0 {
				t.Fatalf("%s tier=%d 一次都没出，需求要求每种品质概率都不能为 0", c.kind, tier)
			}
		}
		// 不应出现权重表之外的档位（含被保底吃掉的低品质档）
		for tier := range hit {
			if _, ok := c.want[tier]; !ok {
				t.Fatalf("%s 出现未配置档位 %d（保底档以下不应产出）", c.kind, tier)
			}
		}
		// 递减校验：按档位从低到高，品质越高概率越低
		tiers := make([]int, 0, len(c.want))
		for tier := range c.want {
			tiers = append(tiers, tier)
		}
		sort.Ints(tiers)
		for i := 0; i < len(tiers)-1; i++ {
			lo, hi := tiers[i], tiers[i+1]
			if c.want[lo] <= c.want[hi] {
				t.Fatalf("%s 档位概率未递减：tier%d=%.0f%% 应大于 tier%d=%.0f%%",
					c.kind, lo, c.want[lo], hi, c.want[hi])
			}
		}
		highQuality = append(highQuality, float64(hit[4]+hit[5])/N*100)
	}
	// 高品质概率：普通 < 精英 < 首领
	for i := 1; i < len(highQuality); i++ {
		if highQuality[i] <= highQuality[i-1] {
			t.Fatalf("高品质概率未随怪物档次提升：%s(%.1f%%) 不大于 %s(%.1f%%)",
				cases[i].kind, highQuality[i], cases[i-1].kind, highQuality[i-1])
		}
	}
	t.Logf("高品质(钻+水晶)概率：normal %.1f%% / elite %.1f%% / boss %.1f%%",
		highQuality[0], highQuality[1], highQuality[2])
}

// v1.9.57 保底档（floor）解析与权重合并：低于 floor 的权重必须并入 floor，
// 等效「保底箱概率 = 自身权重 + 所有更低品质权重之和」。
func TestDropChestFloorMerge(t *testing.T) {
	if err := Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	raw, _ := C().Economy.Drop["monsterChest"].(map[string]any)
	cases := []struct {
		kind  string
		floor string
		want  map[string]float64 // 品质名 -> 合并后占比（%）
	}{
		{"normal", "wood", map[string]float64{"wood": 50, "silver": 28, "gold": 13, "diamond": 6, "crystal": 3}},
		{"elite", "silver", map[string]float64{"wood": 0, "silver": 67, "gold": 17, "diamond": 10, "crystal": 6}},
		{"boss", "gold", map[string]float64{"wood": 0, "silver": 0, "gold": 76, "diamond": 14, "crystal": 10}},
	}
	names := []string{"wood", "silver", "gold", "diamond", "crystal"}
	for _, c := range cases {
		w, ok := raw[c.kind].(map[string]any)
		if !ok {
			t.Fatalf("缺少 %s 档配置", c.kind)
		}
		if got, _ := w["floor"].(string); got != c.floor {
			t.Fatalf("%s 的 floor 应为 %q，实际 %q", c.kind, c.floor, got)
		}
		if got := tierIndexByName(c.floor); got != map[string]int{"wood": 1, "silver": 2, "gold": 3}[c.floor] {
			t.Fatalf("tierIndexByName(%q)=%d 不正确", c.floor, got)
		}
		// 按代码同样的规则做合并（不依赖随机数，直接验证权重口径）
		total, vals := 0.0, make([]float64, len(names))
		for i, n := range names {
			vals[i] = numOf(w, n, 0)
			total += vals[i]
		}
		floor := tierIndexByName(c.floor)
		for i := 0; i < floor-1; i++ {
			vals[floor-1] += vals[i]
			vals[i] = 0
		}
		if total != 100 {
			t.Fatalf("%s 原始权重合计应为 100，实际 %.0f", c.kind, total)
		}
		for i, n := range names {
			got, want := vals[i]/total*100, c.want[n]
			if diff := got - want; diff > 1e-9 || diff < -1e-9 {
				t.Fatalf("%s 合并后 %s 应为 %.0f%%，实际 %.2f%%", c.kind, n, want, got)
			}
		}
	}
	// 未知/空品质名按木箱兜底（配置写错不至于把箱子吞掉）
	for _, bad := range []string{"", "WOOD", "木头", "legendary"} {
		if got := tierIndexByName(bad); got != 1 {
			t.Fatalf("tierIndexByName(%q) 应兜底为 1（木箱），实际 %d", bad, got)
		}
	}
}

// v1.9.46 地图产出系数：每张图比上一张多 20%（1.0 / 1.2 / 1.44 …），未知地图按 1.0。
func TestWorldRewardMult(t *testing.T) {
	if err := Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	want := map[string]float64{"W1": 1.0, "W2": 1.2, "W3": 1.44, "W4": 1.728, "W5": 2.0736, "W6": 2.48832}
	for id, exp := range want {
		if got := WorldRewardMult(id); got != exp {
			t.Fatalf("%s 产出系数 %.5f，期望 %.5f", id, got, exp)
		}
	}
	// 逐图递增 20%
	prev := 0.0
	for _, id := range []string{"W1", "W2", "W3", "W4", "W5", "W6"} {
		cur := WorldRewardMult(id)
		if prev > 0 {
			ratio := cur / prev
			if diff := ratio - 1.2; diff > 1e-6 || diff < -1e-6 {
				t.Fatalf("%s 相对上一张的比值 %.4f，期望 1.2（每图 +20%%）", id, ratio)
			}
		}
		prev = cur
	}
	if got := WorldRewardMult("NOT_EXIST"); got != 1 {
		t.Fatalf("未知地图应回退 1.0，实际 %.2f", got)
	}
}

// v1.9.46 DropKindOf 归一化：只认 elite/boss，其余（含空串与非法值）一律 normal。
func TestDropKindOf(t *testing.T) {
	cases := map[string]string{"normal": "normal", "elite": "elite", "boss": "boss", "": "normal", "ELITE": "normal", "xxx": "normal"}
	for in, want := range cases {
		if got := DropKindOf(in); got != want {
			t.Fatalf("DropKindOf(%q)=%q，期望 %q", in, got, want)
		}
	}
}
