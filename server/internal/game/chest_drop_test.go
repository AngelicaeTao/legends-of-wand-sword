package game

import (
	"math/rand"
	"testing"
)

// v1.9.46 三档掉落权重（economy.drop.monsterChest）：
//   - 每档内部按 木>银>金>钻石>水晶 严格递减，且每种品质概率都 > 0（不能出现必不出）
//   - 怪物档次越高，高品质（钻石+水晶）概率越高：普通 9% < 精英 16% < 首领 24%
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
		{"elite", map[int]float64{1: 40, 2: 27, 3: 17, 4: 10, 5: 6}},
		{"boss", map[int]float64{1: 30, 2: 26, 3: 20, 4: 14, 5: 10}},
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
		// 不应出现权重表之外的档位
		for tier := range hit {
			if _, ok := c.want[tier]; !ok {
				t.Fatalf("%s 出现未配置档位 %d", c.kind, tier)
			}
		}
		// 递减校验：木 > 银 > 金 > 钻石 > 水晶
		for tier := 1; tier < 5; tier++ {
			if c.want[tier] <= c.want[tier+1] {
				t.Fatalf("%s 档位概率未递减：tier%d=%.0f%% 应大于 tier%d=%.0f%%",
					c.kind, tier, c.want[tier], tier+1, c.want[tier+1])
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
