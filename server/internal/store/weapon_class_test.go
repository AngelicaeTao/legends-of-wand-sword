package store

import (
	"testing"

	"zjcs/server/internal/game"
)

// v1.9.38 武器分系：战士系/法师系武器互不通用
func TestWeaponClassSplit(t *testing.T) {
	if err := game.Init("../../../configs"); err != nil { t.Fatal(err) }

	// 1) 战士穿法师武器 → 拒绝
	w := NewPlayer("t_w", "战士", "warrior")
	w.Equip = append(w.Equip, Equip{ID: "m_staff", Slot: "weapon", Quality: 2,
		Stats: map[string]float64{"atk": 12}, ClassID: "mage"})
	if _, err := w.EquipItem("m_staff"); err == nil {
		t.Fatal("战士穿法师武器应被拒绝")
	} else {
		t.Logf("战士穿法师武器拒绝: %v", err)
	}

	// 2) 战士穿战士系武器 → 成功，同槽旧武器自动卸下
	w.Equip = append(w.Equip, Equip{ID: "w_blade", Slot: "weapon", Quality: 1,
		Stats: map[string]float64{"atk": 11}, ClassID: "warrior"})
	if _, err := w.EquipItem("w_blade"); err != nil {
		t.Fatalf("战士穿战士系武器失败: %v", err)
	}

	// 3) 法师穿战士武器 → 拒绝；穿法师武器 → 成功
	m := NewPlayer("t_m", "法师", "mage")
	m.Equip = append(m.Equip, Equip{ID: "w_blade2", Slot: "weapon", Quality: 1,
		Stats: map[string]float64{"atk": 11}, ClassID: "warrior"})
	if _, err := m.EquipItem("w_blade2"); err == nil {
		t.Fatal("法师穿战士系武器应被拒绝")
	} else {
		t.Logf("法师穿战士武器拒绝: %v", err)
	}
	m.Equip = append(m.Equip, Equip{ID: "m_staff2", Slot: "weapon", Quality: 2,
		Stats: map[string]float64{"atk": 12}, ClassID: "mage"})
	if _, err := m.EquipItem("m_staff2"); err != nil {
		t.Fatalf("法师穿法师武器失败: %v", err)
	}

	// 4) 初始武器按职业发放：战士=练习剑(warrior系)、法师=学徒法杖(mage系)
	if w.Equip[0].ID != "练习剑" || w.Equip[0].ClassID != "warrior" {
		t.Fatalf("战士初始武器不符: %+v", w.Equip[0])
	}
	if m.Equip[0].ID != "学徒法杖" || m.Equip[0].ClassID != "mage" {
		t.Fatalf("法师初始武器不符: %+v", m.Equip[0])
	}

	// 5) 老档迁移：无 ClassID 的武器按当前职业归系
	old := NewPlayer("t_old", "老玩家", "mage")
	old.Equip[0].ClassID = "" // 模拟老档
	if !old.MigrateWeaponClass() {
		t.Fatal("迁移应检测到变更")
	}
	if old.Equip[0].ClassID != "mage" {
		t.Fatalf("老档武器应归入 mage 系，实际 %s", old.Equip[0].ClassID)
	}

	// 6) 战斗掉落武器按玩家职业打系标
	war := game.Avatar{Level: 5, ClassID: "warrior"}
	weaponSeen, mgSeen := 0, 0
	for seed := int64(1); seed <= 40; seed++ {
		rep := game.ResolveBattle(game.BattleRequest{MonsterID: "slime", MonsterLv: 5, Seed: seed, Avatar: war})
		for _, d := range rep.Drops {
			if d.Slot == "weapon" {
				weaponSeen++
				if d.ClassID != "warrior" { t.Fatalf("战士掉落武器系别错误: %+v", d) }
			} else if d.ClassID != "" {
				t.Fatalf("防具不应有系标: %+v", d)
			}
		}
	}
	for seed := int64(1); seed <= 40; seed++ {
		rep := game.ResolveBattle(game.BattleRequest{MonsterID: "slime", MonsterLv: 5, Seed: seed, Avatar: game.Avatar{Level: 5, ClassID: "mage"}})
		for _, d := range rep.Drops {
			if d.Slot == "weapon" { mgSeen++; if d.ClassID != "mage" { t.Fatalf("法师掉落武器系别错误: %+v", d) } }
		}
	}
	if weaponSeen == 0 || mgSeen == 0 { t.Fatalf("80 次战斗未覆盖武器掉落（战%d/法%d）", weaponSeen, mgSeen) }
	t.Logf("战斗掉落武器系标校验通过（战士 %d 件 / 法师 %d 件）", weaponSeen, mgSeen)
}
