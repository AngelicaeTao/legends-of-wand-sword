// 存档变更纯函数：把服务端权威结算结果合并进 PlayerState。
// 刻意只依赖 game 包的公开类型与纯函数，不碰 Nakama / 数据库，便于单测与复用。
package store

import (
	"fmt"

	"zjcs/server/internal/game"
)

// ApplyBattleReport 把权威战报的奖励并入存档，返回事件日志。
// 战斗结果由服务端结算，客户端只上传意图，因此这里只做"入账"。
func ApplyBattleReport(p *PlayerState, rep game.BattleReport) []string {
	logs := []string{}
	// v1.9.27 全局血量：战斗结束血量写回存档（战败保底 1 已在战报层兜底）
	if rep.HeroHPEnd > 0 {
		p.Core.HP = rep.HeroHPEnd
	}
	if rep.Result != "win" {
		logs = append(logs, fmt.Sprintf("战败：无奖励，体力照常消耗，剩余血量 %.0f（不自动回满）", p.Core.HP))
		return logs
	}
	p.Core.Exp += rep.Exp
	p.Core.Gold += rep.Gold
	logs = append(logs, fmt.Sprintf("战斗胜利：经验+%d 金币+%d（剩余血量 %.0f）", rep.Exp, rep.Gold, p.Core.HP))

	for _, d := range rep.Drops {
		p.Equip = append(p.Equip, Equip{
			ID: d.ID, Slot: d.Slot, Quality: d.Quality, Enhance: d.Enhance,
			Stats: d.Stats, Equipped: false, ClassID: d.ClassID,
		})
		// v1.9.27 日志显示中文名（品质+部位），不再显示内部 ID 乱码
		logs = append(logs, fmt.Sprintf("获得装备：%s", equipCN(d.Quality, d.Slot, d.ClassID)))
	}

	// 连续升级（服务端判定，防客户端伪造等级）
	for p.Core.Level < 200 && p.Core.Exp >= game.ExpNeeded(p.Core.Level) {
		p.Core.Exp -= game.ExpNeeded(p.Core.Level)
		p.Core.Level++
		logs = append(logs, fmt.Sprintf("升级至 Lv.%d", p.Core.Level))
	}
	p.refreshPower()
	p.appendLogs(logs)
	return logs
}

// ApplyIdleResult 把离线收益并入存档并推进 lastSeen 到 now。
// now 必须是服务端时间，调用方负责传入 ServerNow()。
// v1.9.27：离线挂机额外产出回血药水；离线休息回满血量。
func ApplyIdleResult(p *PlayerState, r game.IdleResult, now int64, maxHp float64) {
	p.Core.Exp += r.Exp
	p.Core.Gold += r.Gold
	p.Core.Material += r.Material
	if r.Potions > 0 {
		addConsumable(p, "potion_s", r.Potions)
	}
	if maxHp > 0 {
		p.Core.HP = maxHp // 离线休息自然回血：血量回满（防 1 血软锁）
	}
	if now > p.World.LastSeen {
		p.World.LastSeen = now
	}
	for p.Core.Level < 200 && p.Core.Exp >= game.ExpNeeded(p.Core.Level) {
		p.Core.Exp -= game.ExpNeeded(p.Core.Level)
		p.Core.Level++
	}
	p.refreshPower()
	extra := ""
	if r.Potions > 0 {
		extra = fmt.Sprintf(" 回血药水+%d（血量已回满）", r.Potions)
	} else if maxHp > 0 {
		extra = "（离线休息，血量已回满）"
	}
	p.appendLogs([]string{fmt.Sprintf("离线结算 %.2fh：经验+%d 金币+%d 材料+%d%s", r.Hours, r.Exp, r.Gold, r.Material, extra)})
}

// addConsumable 把消耗品并入仓库（已有同 id 则累加数量，否则新增一格）。
func addConsumable(p *PlayerState, id string, qty int) {
	if qty <= 0 {
		return
	}
	for i := range p.Warehouse {
		if p.Warehouse[i].Kind == "consume" && p.Warehouse[i].ID == id {
			p.Warehouse[i].Qty += qty
			return
		}
	}
	p.Warehouse = append(p.Warehouse, Item{ID: id, Qty: qty, Kind: "consume"})
}

// ApplyPotionPick 地图草药采集奖励入仓（对应 /api/potion/pick）。
func ApplyPotionPick(p *PlayerState, potions int, gold int) []string {
	logs := []string{}
	if potions > 0 {
		addConsumable(p, "potion_s", potions)
		logs = append(logs, fmt.Sprintf("采集草药：回血药水+%d", potions))
	}
	if gold > 0 {
		p.Core.Gold += gold
		logs = append(logs, fmt.Sprintf("采集草药：金币+%d", gold))
	}
	p.appendLogs(logs)
	return logs
}

// UsePotion 使用回血药水：校验持有、扣库存、回血（不超上限）。
// 返回实际回复量；血量已满返回错误。
// potionAlias：药水 id 归一（v1.9.34）——旧档初始占位"药水"/"回血药水"与配置键 potion_s/potion_l 等价，
// 任何一处持有都可使用；扣减顺序 仓库 → 背包（Ext.bag）。
var potionAlias = map[string]bool{"potion_s": true, "potion_l": true, "药水": true, "回血药水": true}

func isPotionID(id string) bool { return potionAlias[id] }

func numAnyLocal(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case int64:
		return float64(x)
	}
	return 0
}

func (p *PlayerState) UsePotion(itemID string, healPct float64, maxHp float64) (float64, error) {
	if maxHp <= 0 {
		return 0, fmt.Errorf("属性计算异常，请稍后再试")
	}
	// v1.9.41 满血检测修复：先把血量截断到当前最大血量（换装/降级后可能残留 HP>maxHp，
	// 否则永久误判"血量已满"且血条溢出），再做满血判定。
	if p.Core.HP > maxHp {
		p.Core.HP = maxHp
	}
	if p.Core.HP >= maxHp {
		return 0, fmt.Errorf("血量已满（%.0f/%.0f），无需使用药水", p.Core.HP, maxHp)
	}
	idx := -1
	for i := range p.Warehouse {
		if p.Warehouse[i].Kind == "consume" && isPotionID(p.Warehouse[i].ID) && p.Warehouse[i].Qty > 0 {
			idx = i
			break
		}
	}
	if idx >= 0 {
		heal := maxHp * healPct
		if p.Core.HP+heal > maxHp {
			heal = maxHp - p.Core.HP
		}
		p.Warehouse[idx].Qty--
		if p.Warehouse[idx].Qty <= 0 {
			p.Warehouse = append(p.Warehouse[:idx], p.Warehouse[idx+1:]...)
		}
		p.Core.HP = roundHp(p.Core.HP + heal)
		p.appendLogs([]string{fmt.Sprintf("使用回血药水：血量回复 %.0f（当前 %.0f/%.0f）", heal, p.Core.HP, maxHp)})
		return heal, nil
	}
	// 仓库没有 → 从背包（Ext.bag）扣（v1.9.34：背包药水随时可用）
	bag, ok := p.Ext["bag"].(map[string]any)
	if ok {
		for k, v := range bag {
			if isPotionID(k) && numAnyLocal(v) >= 1 {
				bag[k] = numAnyLocal(v) - 1
				heal := maxHp * healPct
				if p.Core.HP+heal > maxHp {
					heal = maxHp - p.Core.HP
				}
				p.Core.HP = roundHp(p.Core.HP + heal)
				p.appendLogs([]string{fmt.Sprintf("使用回血药水：血量回复 %.0f（当前 %.0f/%.0f）", heal, p.Core.HP, maxHp)})
				return heal, nil
			}
		}
	}
	return 0, fmt.Errorf("没有可用的回血药水（地图采草药或离线挂机可获得）")
}

func roundHp(v float64) float64 {
	if v < 1 {
		return 1
	}
	return float64(int(v*10+0.5)) / 10
}

// equipCN 装备显示中文名（品质+部位），杜绝内部 ID 乱码出现在玩家界面。
// equipCN 装备显示名（v1.9.38：武器按系+品质取专属名，如"精钢战刃"/"符文法杖"）。
func equipCN(quality int, slot, classID string) string {
	if wn := game.WeaponName(classID, quality, slot); wn != "" {
		return game.WeaponClassCN(classID) + "系·" + wn
	}
	qn := "普通"
	if qs := game.C().Equipment.Quality; quality >= 0 && quality < len(qs) {
		qn = qs[quality].Name
	}
	sn := slot
	if n, ok := game.C().Equipment.SlotNames[slot]; ok {
		sn = n
	}
	return qn + sn
}

// ApplyChestReward 把宝箱权威奖励并入存档（对应 /api/chest/open）。
// 奖励数值由服务端按 tier 从 economy.json drop.chest 权威 roll，客户端只传 nodeId。
// v1.9.35 详细掉落：药水入仓库（consume）、装备实例入背包（Equip 未穿戴），
// 同时返回结构化 items 明细（供前端开箱结算弹窗格子展示）。
func ApplyChestReward(p *PlayerState, exp, gold, gem, material, potions int, equips []game.EquipItem) ([]string, []map[string]any) {
	logs := []string{}
	items := []map[string]any{}
	p.Core.Exp += exp
	p.Core.Gold += gold
	p.Core.Gem += gem
	p.Core.Material += material
	logs = append(logs, fmt.Sprintf("开箱：经验+%d 金币+%d 钻石+%d 材料+%d", exp, gold, gem, material))
	if material > 0 {
		items = append(items, map[string]any{"kind": "material", "id": "material", "name": "强化材料", "qty": material})
	}
	if potions > 0 {
		addConsumable(p, "potion_s", potions)
		logs = append(logs, fmt.Sprintf("获得：回血药水 x%d", potions))
		items = append(items, map[string]any{"kind": "potion", "id": "potion_s", "name": "回血药水", "qty": potions})
	}
	for _, d := range equips {
		p.Equip = append(p.Equip, Equip{
			ID: d.ID, Slot: d.Slot, Quality: d.Quality, Enhance: d.Enhance,
			Stats: d.Stats, Equipped: false, ClassID: d.ClassID,
		})
		logs = append(logs, fmt.Sprintf("获得装备：%s", equipCN(d.Quality, d.Slot, d.ClassID)))
		items = append(items, map[string]any{"kind": "equip", "id": d.ID, "name": equipCN(d.Quality, d.Slot, d.ClassID), "slot": d.Slot, "quality": d.Quality, "classId": d.ClassID, "qty": 1})
	}
	for p.Core.Level < 200 && p.Core.Exp >= game.ExpNeeded(p.Core.Level) {
		p.Core.Exp -= game.ExpNeeded(p.Core.Level)
		p.Core.Level++
		logs = append(logs, fmt.Sprintf("升级至 Lv.%d", p.Core.Level))
	}
	p.refreshPower()
	p.appendLogs(logs)
	return logs, items
}

// GainExp 直接加经验并处理升级（任务奖励等非战斗来源使用）。
func (p *PlayerState) GainExp(exp int) {
	if exp <= 0 { return }
	p.Core.Exp += exp
	for p.Core.Level < 200 && p.Core.Exp >= game.ExpNeeded(p.Core.Level) {
		p.Core.Exp -= game.ExpNeeded(p.Core.Level)
		p.Core.Level++
		p.appendLogs([]string{fmt.Sprintf("升级至 Lv.%d", p.Core.Level)})
	}
	p.refreshPower()
}

// refreshPower 用当前职业/等级/装备重算战力，避免存档里战力与属性脱节。
func (p *PlayerState) refreshPower() {
	equip := make([]game.EquipItem, 0, len(p.Equip))
	for _, e := range p.Equip {
		equip = append(equip, game.EquipItem{
			ID: e.ID, Slot: e.Slot, Quality: e.Quality, Enhance: e.Enhance, Stats: e.Stats,
		})
	}
	p.Core.BattlePower = int(game.CalcStats(game.Avatar{
		Level: p.Core.Level, ClassID: p.Core.ClassID, Equip: equip,
	}).Power)
}

func (p *PlayerState) appendLogs(items []string) {
	// 只保留最近 50 条，避免存档无限膨胀
	for _, s := range items {
		p.Logs = append(p.Logs, LogEntry{Event: s})
	}
	if len(p.Logs) > 50 {
		p.Logs = append([]LogEntry{}, p.Logs[len(p.Logs)-50:]...)
	}
}
