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
	if rep.Result != "win" {
		logs = append(logs, "战败：无奖励，体力照常消耗")
		return logs
	}
	p.Core.Exp += rep.Exp
	p.Core.Gold += rep.Gold
	logs = append(logs, fmt.Sprintf("战斗胜利：经验+%d 金币+%d", rep.Exp, rep.Gold))

	for _, d := range rep.Drops {
		p.Equip = append(p.Equip, Equip{
			ID: d.ID, Slot: d.Slot, Quality: d.Quality, Enhance: d.Enhance,
			Stats: d.Stats, Equipped: false,
		})
		logs = append(logs, fmt.Sprintf("获得装备：%s（%s）", d.ID, d.Slot))
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
func ApplyIdleResult(p *PlayerState, r game.IdleResult, now int64) {
	p.Core.Exp += r.Exp
	p.Core.Gold += r.Gold
	p.Core.Material += r.Material
	if now > p.World.LastSeen {
		p.World.LastSeen = now
	}
	for p.Core.Level < 200 && p.Core.Exp >= game.ExpNeeded(p.Core.Level) {
		p.Core.Exp -= game.ExpNeeded(p.Core.Level)
		p.Core.Level++
	}
	p.refreshPower()
	p.appendLogs([]string{fmt.Sprintf("离线结算 %.2fh：经验+%d 金币+%d 材料+%d", r.Hours, r.Exp, r.Gold, r.Material)})
}

// ApplyChestReward 把宝箱权威奖励并入存档（对应 /api/chest/open）。
// 奖励数值由服务端按 tier 从 economy.json drop.chest 权威 roll，客户端只传 nodeId。
func ApplyChestReward(p *PlayerState, exp, gold, gem, material int) []string {
	logs := []string{}
	p.Core.Exp += exp
	p.Core.Gold += gold
	p.Core.Gem += gem
	p.Core.Material += material
	logs = append(logs, fmt.Sprintf("开箱：经验+%d 金币+%d 钻石+%d 材料+%d", exp, gold, gem, material))
	for p.Core.Level < 200 && p.Core.Exp >= game.ExpNeeded(p.Core.Level) {
		p.Core.Exp -= game.ExpNeeded(p.Core.Level)
		p.Core.Level++
		logs = append(logs, fmt.Sprintf("升级至 Lv.%d", p.Core.Level))
	}
	p.refreshPower()
	p.appendLogs(logs)
	return logs
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
