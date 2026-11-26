package game

import (
	"math/rand"
	"testing"
)

// v1.9.45 怪物掉落宝箱档位权重：服务端权威 roll，普通/精英与 BOSS 分别校验。
// 期望值取自 configs/economy.json drop.monsterChest：
// normal 木47/银30/金8/钻12/水晶3；boss 金50/钻30/水晶20。
func TestDropChestTierWeights(t *testing.T) {
	if err := Init("../../configs_shared"); err != nil {
		t.Fatal(err)
	}
	const N = 200000
	cases := []struct {
		name  string
		boss  bool
		want  map[int]float64 // tier -> 期望占比（%）
		tol   float64         // 允许的绝对误差（百分点）
	}{
		{"normal", false, map[int]float64{1: 47, 2: 30, 3: 8, 4: 12, 5: 3}, 1.2},
		{"boss", true, map[int]float64{3: 50, 4: 30, 5: 20}, 1.5},
	}
	for _, c := range cases {
		sm := &SpawnManager{rand: rand.New(rand.NewSource(20251126))}
		hit := map[int]int{}
		for i := 0; i < N; i++ {
			hit[sm.rollDropTier(c.boss)]++
		}
		for tier, want := range c.want {
			got := float64(hit[tier]) / N * 100
			if diff := got - want; diff > c.tol || diff < -c.tol {
				t.Fatalf("%s tier=%d 实测 %.2f%% 偏离期望 %.0f%%（容差 %.1f）", c.name, tier, got, want, c.tol)
			}
		}
		// 不应出现权重表之外的档位
		for tier := range hit {
			if _, ok := c.want[tier]; !ok {
				t.Fatalf("%s 出现未配置档位 %d", c.name, tier)
			}
		}
	}
}
