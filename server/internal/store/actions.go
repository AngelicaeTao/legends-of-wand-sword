// 玩家操作的存档变更纯函数：服务端权威校验 + 入账，客户端只传意图。
// 对应需求文档第十章协议表与 Bug 报告 B05/B04 修复。
package store

import (
	"fmt"
	"time"

	"zjcs/server/internal/game"
)

// SubmitBD 提交战前技能配置（4槽）。仅允许已解锁技能，最多 4 个，派生互斥：
// 同一流派核心技不重复堆叠（此处以 element 去重，保守策略），服务端权威写入。
func (p *PlayerState) SubmitBD(slots []string) ([]string, error) {
	maxSlots := game.C().Skills.Slots
	if maxSlots <= 0 {
		maxSlots = 4
	}
	if len(slots) > maxSlots {
		return nil, fmt.Errorf("技能槽最多 %d 个", maxSlots)
	}
	// 校验每个技能已解锁 + element 互斥
	unlocked := map[string]bool{}
	for _, id := range p.Skills.Unlocked {
		unlocked[id] = true
	}
	seenElem := map[string]bool{}
	out := []string{}
	for _, id := range slots {
		if id == "basic" {
			continue // 普攻系统内置，不占提交槽
		}
		if !unlocked[id] {
			return nil, fmt.Errorf("技能未解锁：%s", id)
		}
		sk, ok := game.C().SkillByID(id)
		if !ok {
			return nil, fmt.Errorf("技能不存在：%s", id)
		}
		elem := sk.Element
		if elem == "" {
			elem = "none"
		}
		// 互斥：非 none 的同元素技能不可同时装备（流派互斥的保守实现）
		if elem != "none" && seenElem[elem] {
			return nil, fmt.Errorf("同流派技能互斥：%s", sk.Name)
		}
		seenElem[elem] = true
		out = append(out, id)
	}
	p.Skills.Slots = out
	p.appendLogs([]string{fmt.Sprintf("更新技能BD：%d 个技能", len(out))})
	return out, nil
}

// UnlockSkill 解锁技能（供成长/转职解锁，演示用：花钻石），服务端权威扣费。
func (p *PlayerState) UnlockSkill(id string, gemCost int) (bool, error) {
	if _, ok := game.C().SkillByID(id); !ok {
		return false, fmt.Errorf("技能不存在：%s", id)
	}
	for _, u := range p.Skills.Unlocked {
		if u == id {
			return false, nil // 已解锁
		}
	}
	if p.Core.Gem < gemCost {
		return false, fmt.Errorf("钻石不足（需 %d）", gemCost)
	}
	p.Core.Gem -= gemCost
	p.Skills.Unlocked = append(p.Skills.Unlocked, id)
	p.appendLogs([]string{fmt.Sprintf("解锁技能：%s（-%d 钻石）", id, gemCost)})
	return true, nil
}

// EquipItem 穿戴装备（装备实例统一存 p.Equip，Equipped 标志区分穿戴/仓库）。
// 同一槽位原装备自动卸下（Equipped=false，退回仓库视图）。服务端权威。
func (p *PlayerState) EquipItem(itemID string) ([]string, error) {
	target := -1
	for i, e := range p.Equip {
		if e.ID == itemID {
			target = i
			break
		}
	}
	if target < 0 {
		return nil, fmt.Errorf("仓库中无此装备：%s", itemID)
	}
	slot := p.Equip[target].Slot
	logs := []string{}
	// 同槽位其他装备先卸下
	for i := range p.Equip {
		if i != target && p.Equip[i].Equipped && p.Equip[i].Slot == slot {
			p.Equip[i].Equipped = false
			logs = append(logs, fmt.Sprintf("卸下：%s", p.Equip[i].ID))
		}
	}
	p.Equip[target].Equipped = true
	logs = append(logs, fmt.Sprintf("穿戴：%s", itemID))
	p.refreshPower()
	p.appendLogs(logs)
	return logs, nil
}

// UnequipItem 卸下装备（Equipped=false，退回仓库视图）。
func (p *PlayerState) UnequipItem(itemID string) ([]string, error) {
	for i := range p.Equip {
		if p.Equip[i].ID == itemID {
			if !p.Equip[i].Equipped {
				return nil, fmt.Errorf("该装备未在穿戴中：%s", itemID)
			}
			p.Equip[i].Equipped = false
			p.refreshPower()
			p.appendLogs([]string{fmt.Sprintf("卸下装备：%s", itemID)})
			return []string{fmt.Sprintf("卸下：%s", itemID)}, nil
		}
	}
	return nil, fmt.Errorf("未找到装备：%s", itemID)
}

// EnhanceEquip 强化装备（必成功，消耗金币+材料），战力即时更新。
func (p *PlayerState) EnhanceEquip(slot string, goldCost, matCost int) (int, error) {
	for i := range p.Equip {
		if p.Equip[i].Slot == slot {
			if p.Core.Gold < goldCost || p.Core.Material < matCost {
				return 0, fmt.Errorf("资源不足（需 %d 金币 / %d 材料）", goldCost, matCost)
			}
			p.Core.Gold -= goldCost
			p.Core.Material -= matCost
			p.Equip[i].Enhance++
			p.refreshPower()
			p.appendLogs([]string{fmt.Sprintf("强化 %s 至 +%d", slot, p.Equip[i].Enhance)})
			return p.Equip[i].Enhance, nil
		}
	}
	return 0, fmt.Errorf("该槽位无装备：%s", slot)
}

// WarehouseDeposit 把背包物品存入仓库（演示：把装备实例转为仓库条目由 EquipItem 逆向完成，此处处理普通材料）。
func (p *PlayerState) WarehouseDeposit(itemID string, qty int) error {
	if qty <= 0 {
		return fmt.Errorf("数量无效")
	}
	// 从 Ext.bag 扣除（若存在），否则从 Core 资源扣除
	bag, _ := p.Ext["bag"].(map[string]any)
	if bag != nil {
		if cur, ok := bag[itemID].(float64); ok && int(cur) >= qty {
			bag[itemID] = cur - float64(qty)
		} else {
			return fmt.Errorf("背包数量不足")
		}
	} else {
		return fmt.Errorf("背包为空")
	}
	for i := range p.Warehouse {
		if p.Warehouse[i].ID == itemID {
			p.Warehouse[i].Qty += qty
			p.appendLogs([]string{fmt.Sprintf("存入仓库：%s x%d", itemID, qty)})
			return nil
		}
	}
	p.Warehouse = append(p.Warehouse, Item{ID: itemID, Qty: qty, Kind: "material"})
	p.appendLogs([]string{fmt.Sprintf("存入仓库：%s x%d", itemID, qty)})
	return nil
}

// WarehouseWithdraw 从仓库取出物品到背包。
func (p *PlayerState) WarehouseWithdraw(itemID string, qty int) error {
	for i := range p.Warehouse {
		if p.Warehouse[i].ID == itemID {
			if p.Warehouse[i].Qty < qty {
				return fmt.Errorf("仓库数量不足")
			}
			p.Warehouse[i].Qty -= qty
			if p.Warehouse[i].Qty <= 0 {
				p.Warehouse = append(p.Warehouse[:i], p.Warehouse[i+1:]...)
			}
			bag, ok := p.Ext["bag"].(map[string]any)
			if !ok {
				bag = map[string]any{}
				p.Ext["bag"] = bag
			}
			bag[itemID] = numAny(bag[itemID]) + float64(qty)
			p.appendLogs([]string{fmt.Sprintf("取出：%s x%d", itemID, qty)})
			return nil
		}
	}
	return fmt.Errorf("仓库中无此物品：%s", itemID)
}

// DungeonEnter 副本进入：服务端权威每日次数校验与扣减。
// 返回 (剩余次数, error)。每日 0 点（服务端时间）重置。
func (p *PlayerState) DungeonEnter(dungeonID string, perDay int) (int, error) {
	if perDay <= 0 {
		return 0, fmt.Errorf("副本次数配置无效")
	}
	today := time.Unix(game.ServerNow(), 0).Format("2006-01-02")
	counter, _ := p.Ext["dungeonCounter"].(map[string]any)
	if counter == nil {
		counter = map[string]any{}
		p.Ext["dungeonCounter"] = counter
	}
	Stamp := fmt.Sprintf("%v", counter["date"])
	if Stamp != today {
		counter = map[string]any{"date": today}
		p.Ext["dungeonCounter"] = counter
	}
	used := 0
	if v, ok := counter[dungeonID].(float64); ok {
		used = int(v)
	}
	if used >= perDay {
		return 0, fmt.Errorf("今日次数已用完（%d/%d）", used, perDay)
	}
	counter[dungeonID] = float64(used + 1)
	p.appendLogs([]string{fmt.Sprintf("进入副本 %s（%d/%d）", dungeonID, used+1, perDay)})
	return perDay - used - 1, nil
}

// DungeonRemain 查询副本今日剩余次数。
func (p *PlayerState) DungeonRemain(dungeonID string, perDay int) int {
	today := time.Unix(game.ServerNow(), 0).Format("2006-01-02")
	counter, _ := p.Ext["dungeonCounter"].(map[string]any)
	if counter == nil || fmt.Sprintf("%v", counter["date"]) != today {
		return perDay
	}
	used := 0
	if v, ok := counter[dungeonID].(float64); ok {
		used = int(v)
	}
	r := perDay - used
	if r < 0 {
		r = 0
	}
	return r
}

func numAny(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	default:
		return 0
	}
}
