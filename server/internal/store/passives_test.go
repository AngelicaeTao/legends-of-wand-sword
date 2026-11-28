package store

import (
	"testing"

	"zjcs/server/internal/game"
)

// v1.9.89 被动装配校验：转职后才可装配，且只能装当前职业的被动。
func TestSavePassivesValidation(t *testing.T) {
	if err := game.Init("../../../configs"); err != nil {
		t.Fatal(err)
	}

	// ① 一阶职业（战士）没有被动池 → 任何被动都应被拒
	w := NewPlayer("t_pas_warrior", "小战士", "warrior")
	if _, err := w.SavePassives([]string{"kp_bulwark"}); err == nil {
		t.Fatal("战士不应能装配骑士被动")
	}

	// ② 骑士：装 2 个合法被动 → 成功，且战力包含被动固定分
	k := NewPlayer("t_pas_knight", "小骑士", "knight")
	k.refreshPower() // 新角色创建时 BattlePower 还是 0，先算一次再取基准
	basePower := k.Core.BattlePower
	got, err := k.SavePassives([]string{"kp_bulwark", "kp_thorns"})
	if err != nil {
		t.Fatalf("装配 2 个合法被动应成功，实际 %v", err)
	}
	if len(got) != 2 || len(k.Skills.Passives) != 2 {
		t.Fatalf("被动应有 2 个，实际 %v", got)
	}
	if k.Core.BattlePower != basePower+160 {
		t.Fatalf("战力应 +160（2 × 80 固定分），实际 %+d", k.Core.BattlePower-basePower)
	}

	// ③ 超过槽位上限 → 拒绝
	if _, err := k.SavePassives([]string{"kp_bulwark", "kp_thorns", "kp_guardian"}); err == nil {
		t.Fatal("超过 2 个被动应被拒绝")
	}

	// ④ 主动技能冒充被动 → 拒绝
	if _, err := k.SavePassives([]string{"w_slash"}); err == nil {
		t.Fatal("主动技能不应能装进被动槽")
	}

	// ⑤ 跨职业被动 → 拒绝
	if _, err := k.SavePassives([]string{"wp_bloodpact"}); err == nil {
		t.Fatal("骑士不应能装术士被动")
	}

	// ⑥ 不存在的 id → 拒绝
	if _, err := k.SavePassives([]string{"not_exist"}); err == nil {
		t.Fatal("不存在的被动应被拒绝")
	}

	// ⑦ 重复装配去重（不报错，落库只留 1 个）
	dedup, err := k.SavePassives([]string{"kp_bulwark", "kp_bulwark"})
	if err != nil || len(dedup) != 1 {
		t.Fatalf("重复被动应去重为 1 个，实际 %v err=%v", dedup, err)
	}

	// ⑧ 传空数组表示清空
	cleared, err := k.SavePassives(nil)
	if err != nil || len(cleared) != 0 {
		t.Fatalf("清空被动应成功，实际 %v err=%v", cleared, err)
	}
	if k.Core.BattlePower != basePower {
		t.Fatalf("清空被动后战力应回到 %d，实际 %d", basePower, k.Core.BattlePower)
	}
	t.Logf("被动校验：一阶拒绝 / 2 槽上限 / 主动冒充拒绝 / 跨职业拒绝 / 去重 / 清空 ✓")
}

// v1.9.89 转职：换职业后旧被动必须自动卸下（不同职业被动不通用）。
func TestTransferDropsForeignPassives(t *testing.T) {
	if err := game.Init("../../../configs"); err != nil {
		t.Fatal(err)
	}
	p := NewPlayer("t_pas_transfer", "转职者", "warrior")
	p.Core.Level = 60
	p.World.Unlocked = []string{"W1", "W2"}
	// 先转骑士并装上骑士被动
	if err := p.TransferTo("knight", 2); err != nil {
		t.Fatalf("转骑士失败：%v", err)
	}
	if _, err := p.SavePassives([]string{"kp_bulwark", "kp_thorns"}); err != nil {
		t.Fatalf("骑士装配被动失败：%v", err)
	}
	// 再转拳师：骑士被动不在拳师池里，应被清空
	if err := p.TransferTo("fighter", 2); err != nil {
		t.Fatalf("转拳师失败：%v", err)
	}
	if len(p.Skills.Passives) != 0 {
		t.Fatalf("转职后旧职业被动应被清空，实际 %v", p.Skills.Passives)
	}
	// 拳师可以正常装自己的被动
	if _, err := p.SavePassives([]string{"fp_swift", "fp_frenzy"}); err != nil {
		t.Fatalf("拳师装配自身被动失败：%v", err)
	}
	t.Logf("转职清理：骑士被动 → 拳师后清空，可重新装配拳师被动 ✓")
}
