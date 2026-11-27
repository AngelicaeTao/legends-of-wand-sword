// 玩家操作的存档变更纯函数：服务端权威校验 + 入账，客户端只传意图。
// 对应需求文档第十章协议表与 Bug 报告 B05/B04 修复。
package store

import (
	"fmt"
	"time"

	"zjcs/server/internal/game"
)

// SubmitBD 提交战前技能配置（默认 4 槽）。服务端权威校验：
//  1. 技能必须已解锁；
//  2. 技能必须属于当前职业的 BD 池（initial ∪ advanced）——交付包 §4.3「不显示/不允许跨职业技能」；
//  3. 不超过槽位上限，且不重复装配。
func (p *PlayerState) SubmitBD(slots []string) ([]string, error) {
	c := game.C()
	maxSlots := c.Skills.Slots
	if maxSlots <= 0 {
		maxSlots = 4
	}
	if len(slots) > maxSlots {
		return nil, fmt.Errorf("技能槽最多 %d 个", maxSlots)
	}
	unlocked := map[string]bool{}
	for _, id := range p.Skills.Unlocked {
		unlocked[id] = true
	}
	out := []string{}
	for _, id := range slots {
		if id == "basic" {
			continue // 普攻系统内置，不占提交槽
		}
		if !unlocked[id] {
			return nil, fmt.Errorf("技能未解锁：%s", id)
		}
		sk, ok := c.SkillByID(id)
		if !ok {
			return nil, fmt.Errorf("技能不存在：%s", id)
		}
		if !c.InBDPool(p.Core.ClassID, id) {
			return nil, fmt.Errorf("跨职业技能不可装配：%s（当前职业技能池外）", sk.Name)
		}
		if containsStr(out, id) {
			continue
		}
		out = append(out, id)
	}
	p.Skills.Slots = out
	p.appendLogs([]string{fmt.Sprintf("更新技能BD：%d 个技能", len(out))})
	return out, nil
}

func containsStr(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// UnlockSkill 解锁技能（供成长/转职解锁，演示用：花钻石），服务端权威扣费。
func (p *PlayerState) UnlockSkill(id string, gemCost int) (bool, error) {
	c := game.C()
	if _, ok := c.SkillByID(id); !ok {
		return false, fmt.Errorf("技能不存在：%s", id)
	}
	if !c.InBDPool(p.Core.ClassID, id) {
		return false, fmt.Errorf("该技能不属于当前职业，无法解锁")
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
	// v1.9.38 武器分系：战士系/法师系武器互不通用（服务端权威校验）
	if it := p.Equip[target]; it.Slot == "weapon" && it.ClassID != "" && it.ClassID != game.WeaponClassFor(p.Core.ClassID) {
		return nil, fmt.Errorf("该武器为%s系专属，当前职业无法装备（打怪/开箱可获得本职业武器）", game.WeaponClassCN(it.ClassID))
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

// QualityUp 装备升品（v1.9.42）：仓库或背包中的装备品质 +1（最高混沌=7），
// stats 按 mult 重算（品质乘数 × 原基值）。消耗由 net 层按配置算好传入。
// 返回新品质档与更新后的属性。
func (p *PlayerState) QualityUp(itemID string, goldCost, matCost int, mults []float64) (int, map[string]float64, error) {
	if p.Core.Gold < goldCost || p.Core.Material < matCost {
		return 0, nil, fmt.Errorf("资源不足（需 %d 金币 / %d 材料）", goldCost, matCost)
	}
	for i := range p.Equip {
		if p.Equip[i].ID == itemID {
			e := &p.Equip[i]
			return qualityUpOne(e, goldCost, matCost, mults, p)
		}
	}
	for i := range p.Warehouse {
		if p.Warehouse[i].Kind == "equip" && p.Warehouse[i].ID == itemID {
			e := p.Warehouse[i].Equip
			if e == nil {
				return 0, nil, fmt.Errorf("装备数据异常")
			}
			q, st, err := qualityUpOne(e, goldCost, matCost, mults, p)
			if err != nil {
				return 0, nil, err
			}
			p.Warehouse[i].Equip = e
			return q, st, nil
		}
	}
	return 0, nil, fmt.Errorf("未找到装备：%s", itemID)
}

func qualityUpOne(e *Equip, goldCost, matCost int, mults []float64, p *PlayerState) (int, map[string]float64, error) {
	maxQ := len(mults) - 1
	if maxQ < 0 {
		maxQ = 7
	}
	if e.Quality >= maxQ {
		return 0, nil, fmt.Errorf("已达最高品质（混沌），无法继续升品")
	}
	// 用旧品质 mult 反推基值，再乘新品质 mult（保留装备成长一致性）
	oldMult := 1.0
	newMult := 1.0
	if e.Quality < len(mults) {
		oldMult = mults[e.Quality]
	}
	if e.Quality+1 < len(mults) {
		newMult = mults[e.Quality+1]
	}
	ratio := 1.0
	if oldMult > 0 {
		ratio = newMult / oldMult
	}
	for k, v := range e.Stats {
		e.Stats[k] = round2f(v * ratio)
	}
	e.Quality++
	p.Core.Gold -= goldCost
	p.Core.Material -= matCost
	p.refreshPower()
	p.appendLogs([]string{fmt.Sprintf("装备升品：%s → 品质+%d（消耗 %d 金币 / %d 材料）", e.ID, e.Quality, goldCost, matCost)})
	return e.Quality, e.Stats, nil
}

func round2f(v float64) float64 { return float64(int(v*100+0.5)) / 100 }

// EnhanceEquip 强化装备（必成功，消耗金币+材料），战力即时更新。
// v1.9.21 支持按装备实例 ID 精确定位（同一槽位可能有多件装备，按槽位强化会打错对象）；
// itemID 为空时按槽位回退（兼容旧链路）。
func (p *PlayerState) EnhanceEquip(itemID, slot string, goldCost, matCost int) (int, error) {
	for i := range p.Equip {
		hit := (itemID != "" && p.Equip[i].ID == itemID) || (itemID == "" && p.Equip[i].Slot == slot)
		if hit {
			if p.Core.Gold < goldCost || p.Core.Material < matCost {
				return 0, fmt.Errorf("资源不足（需 %d 金币 / %d 材料）", goldCost, matCost)
			}
			p.Core.Gold -= goldCost
			p.Core.Material -= matCost
			p.Equip[i].Enhance++
			p.refreshPower()
			p.appendLogs([]string{fmt.Sprintf("强化 %s 至 +%d", p.Equip[i].ID, p.Equip[i].Enhance)})
			return p.Equip[i].Enhance, nil
		}
	}
	if itemID != "" {
		return 0, fmt.Errorf("未找到装备：%s", itemID)
	}
	return 0, fmt.Errorf("该槽位无装备：%s", slot)
}

// ShopExchange 商店兑换（v1.9.53）：用金币/钻石换强化材料或经验。
//
//	item=material|exp，pay=gold|gem，qty=玩家想"得到"的数量（材料件数 / 经验点数）。
//	比例读 configs/economy.json 的 shop 段，缺配置按默认：
//	  材料：10 金币/件、1 钻石/件；经验：1 金币/点、1 钻石 10 点（钻石档必须按 10 的倍数）。
//
// 扣费与发放都在服务端完成，前端只报意图，返回实际消耗与获得量。
func (p *PlayerState) ShopExchange(item, pay string, qty int) (cost, got int, err error) {
	if qty <= 0 {
		return 0, 0, fmt.Errorf("兑换数量需大于 0")
	}
	if max := shopInt("", "maxQty", 1000000); qty > max {
		return 0, 0, fmt.Errorf("单次最多兑换 %d", max)
	}
	switch item {
	case "material":
		switch pay {
		case "gold":
			cost = qty * shopInt("material", "goldPerUnit", 10)
		case "gem":
			cost = qty * shopInt("material", "gemPerUnit", 1)
		default:
			return 0, 0, fmt.Errorf("不支持用 %s 兑换材料", pay)
		}
	case "exp":
		switch pay {
		case "gold":
			cost = qty * shopInt("exp", "goldPerUnit", 1)
		case "gem":
			per := shopInt("exp", "gemPerExp", 10)
			if per <= 0 {
				per = 10
			}
			if qty%per != 0 {
				return 0, 0, fmt.Errorf("钻石兑换经验需按 %d 的倍数（当前 %d）", per, qty)
			}
			cost = qty / per
		default:
			return 0, 0, fmt.Errorf("不支持用 %s 兑换经验", pay)
		}
	default:
		return 0, 0, fmt.Errorf("未知兑换商品：%s", item)
	}
	if cost <= 0 {
		return 0, 0, fmt.Errorf("兑换数量过小")
	}

	switch pay {
	case "gold":
		if p.Core.Gold < cost {
			return 0, 0, fmt.Errorf("金币不足（需 %d，持有 %d）", cost, p.Core.Gold)
		}
		p.Core.Gold -= cost
	case "gem":
		if p.Core.Gem < cost {
			return 0, 0, fmt.Errorf("钻石不足（需 %d，持有 %d）", cost, p.Core.Gem)
		}
		p.Core.Gem -= cost
	}

	switch item {
	case "material":
		p.Core.Material += qty
	case "exp":
		p.GainExp(qty) // 复用升级处理：够经验会自动升级并重算战力
	}
	curCN := map[string]string{"gold": "金币", "gem": "钻石"}[pay]
	itemCN := map[string]string{"material": "强化材料", "exp": "经验"}[item]
	p.appendLogs([]string{fmt.Sprintf("商店兑换：-%d %s → %s +%d", cost, curCN, itemCN, qty)})
	return cost, qty, nil
}

// shopInt 读取 shop 配置的整数比例；section 为空表示读顶层（maxQty）。
func shopInt(section, key string, def int) int {
	cfg := game.C().Economy.Shop
	if cfg == nil {
		return def
	}
	holder := cfg
	if section != "" {
		holder, _ = cfg[section].(map[string]any)
	}
	if holder == nil {
		return def
	}
	if v, ok := holder[key].(float64); ok && v > 0 {
		return int(v)
	}
	return def
}

// ---------- v1.9.56 技能升级 ----------

// SkillLevelOf 技能当前等级：缺省 / 非法值按 1 级（老存档兼容）。
func (p *PlayerState) SkillLevelOf(id string) int {
	if p.Skills.Levels != nil {
		if lv := p.Skills.Levels[id]; lv > 0 {
			return lv
		}
	}
	return 1
}

// SkillUpgrade 技能升级（v1.9.56）：
//   - 只能升已解锁且属于当前职业技能池的技能；
//   - 每级效果 +8%（战斗内对 power/治疗/减伤同乘，见 game.SkillEffectMult）；
//   - 消耗钻石 = baseGem + stepGem×(当前等级-1)，上限 maxLevel；
//   - 扣费在服务端完成，返回升级后等级与实际消耗。
func (p *PlayerState) SkillUpgrade(id string) (level, cost int, err error) {
	c := game.C()
	if _, ok := c.SkillByID(id); !ok {
		return 0, 0, fmt.Errorf("未知技能：%s", id)
	}
	if !c.InBDPool(p.Core.ClassID, id) {
		return 0, 0, fmt.Errorf("该技能不属于当前职业")
	}
	unlocked := false
	for _, x := range p.Skills.Unlocked {
		if x == id {
			unlocked = true
			break
		}
	}
	if !unlocked {
		return 0, 0, fmt.Errorf("技能尚未解锁，先解锁再升级")
	}
	cur := p.SkillLevelOf(id)
	if max := game.SkillMaxLevel(); cur >= max {
		return cur, 0, fmt.Errorf("技能已满级（%d 级）", max)
	}
	cost = game.SkillUpgradeCost(cur)
	if cost <= 0 {
		return cur, 0, fmt.Errorf("技能已满级")
	}
	if p.Core.Gem < cost {
		return cur, cost, fmt.Errorf("钻石不足（需 %d，持有 %d）", cost, p.Core.Gem)
	}
	p.Core.Gem -= cost
	if p.Skills.Levels == nil {
		p.Skills.Levels = map[string]int{}
	}
	p.Skills.Levels[id] = cur + 1
	p.appendLogs([]string{fmt.Sprintf("技能升级：%s → Lv.%d（消耗 %d 钻石）", id, cur+1, cost)})
	return cur + 1, cost, nil
}

// ---------- v1.9.56 背包回收（材料 / 药水 → 金币或钻石） ----------

// sellableKind 判断该物品类别能否回收：材料与药水可回收，装备不在此列。
func sellableKind(kind string) bool {
	switch kind {
	case "材料", "material", "药水", "consume", "potion":
		return true
	}
	return false
}

// sellUnitPrice 单件回收价：优先按物品 id 定价，其次按类别兜底，最后用硬编码保底。
func sellUnitPrice(itemID, kind, pay string) int {
	group := "material"
	if kind == "consume" || kind == "药水" || kind == "potion" {
		group = "consume"
	}
	if cfg := game.C().Economy.Shop; cfg != nil {
		if sell, ok := cfg["sell"].(map[string]any); ok {
			if items, ok := sell["items"].(map[string]any); ok {
				if row, ok := items[itemID].(map[string]any); ok {
					if v, ok := row[pay].(float64); ok && v > 0 {
						return int(v)
					}
				}
			}
			if def, ok := sell["default"].(map[string]any); ok {
				if row, ok := def[group].(map[string]any); ok {
					if v, ok := row[pay].(float64); ok && v > 0 {
						return int(v)
					}
				}
			}
		}
	}
	if group == "consume" {
		if pay == "gold" {
			return 15
		}
		return 1
	}
	if pay == "gold" {
		return 10
	}
	return 1
}

// ShopSell 背包回收（v1.9.56）：把仓库/背包里的材料或药水换成金币或钻石。
//
//	itemID 按 id 匹配（同 id 多堆叠会一起扣减），pay=gold|gem，qty=要卖掉的件数。
//	返回单件价、实际到账总额；数量不足或物品不可回收时报错且不扣任何东西。
func (p *PlayerState) ShopSell(itemID string, qty int, pay string) (unit, got int, err error) {
	if itemID == "" {
		return 0, 0, fmt.Errorf("缺少物品 id")
	}
	if qty <= 0 {
		return 0, 0, fmt.Errorf("回收数量需大于 0")
	}
	if max := shopInt("", "maxQty", 1000000); qty > max {
		return 0, 0, fmt.Errorf("单次最多回收 %d 件", max)
	}
	if pay != "gold" && pay != "gem" {
		return 0, 0, fmt.Errorf("不支持回收成 %s", pay)
	}
	avail, kind := 0, ""
	for _, it := range p.Warehouse {
		if it.ID == itemID && sellableKind(it.Kind) && it.Qty > 0 {
			avail += it.Qty
			if kind == "" {
				kind = it.Kind
			}
		}
	}
	if avail <= 0 {
		return 0, 0, fmt.Errorf("背包里没有可回收的 %s", itemID)
	}
	if avail < qty {
		return 0, 0, fmt.Errorf("数量不足（持有 %d，想卖 %d）", avail, qty)
	}
	unit = sellUnitPrice(itemID, kind, pay)
	if unit <= 0 {
		return 0, 0, fmt.Errorf("该物品暂不支持回收")
	}
	// 扣减：同 id 多堆叠依次扣，扣空即移除（沿用原切片，避免额外分配）
	left := qty
	out := p.Warehouse[:0]
	for _, it := range p.Warehouse {
		if left > 0 && it.ID == itemID && sellableKind(it.Kind) && it.Qty > 0 {
			take := it.Qty
			if take > left {
				take = left
			}
			it.Qty -= take
			left -= take
			if it.Qty <= 0 {
				continue
			}
		}
		out = append(out, it)
	}
	p.Warehouse = out
	got = unit * qty
	curCN := "金币"
	if pay == "gold" {
		p.Core.Gold += got
	} else {
		p.Core.Gem += got
		curCN = "钻石"
	}
	p.appendLogs([]string{fmt.Sprintf("商店回收：%s x%d → %s +%d", itemID, qty, curCN, got)})
	return unit, got, nil
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

// WarehouseEquipIn 背包装备 → 仓库（存一件）。itemId 匹配第一件未穿戴的同名装备，
// 整件搬运（品质/强化等级/属性随行），服务端权威。
func (p *PlayerState) WarehouseEquipIn(itemID string) error {
	for i := range p.Equip {
		if p.Equip[i].ID == itemID && !p.Equip[i].Equipped {
			p.Warehouse = append(p.Warehouse, Item{
				ID: itemID, Qty: 1, Kind: "equip",
				Equip: &Equip{
					ID: p.Equip[i].ID, Slot: p.Equip[i].Slot, Quality: p.Equip[i].Quality,
					Enhance: p.Equip[i].Enhance, Stats: p.Equip[i].Stats,
					ClassID: p.Equip[i].ClassID, // v1.9.40 修复：武器系标随行，取出后分系校验不失效
				},
			})
			p.Equip = append(p.Equip[:i], p.Equip[i+1:]...)
			p.appendLogs([]string{fmt.Sprintf("存入仓库装备：%s", itemID)})
			return nil
		}
	}
	return fmt.Errorf("背包中没有该装备：%s", itemID)
}

// WarehouseDecompose 分解仓库中的装备（v1.9.40 金币 / v1.9.41 加材料返还）：
// 返还金额由 net 层按 economy.drop.decompose 配置算好传入，服务端权威校验与入账。
// 返回实际入账（金币, 材料）。
func (p *PlayerState) WarehouseDecompose(itemID string, goldGain, matGain int) (int, int, error) {
	if goldGain < 0 || matGain < 0 {
		return 0, 0, fmt.Errorf("分解收益配置无效")
	}
	for i := range p.Warehouse {
		if p.Warehouse[i].Kind == "equip" && p.Warehouse[i].ID == itemID {
			e := p.Warehouse[i].Equip
			name := itemID
			if e != nil {
				name = e.ID
			}
			p.Core.Gold += goldGain
			p.Core.Material += matGain
			p.Warehouse = append(p.Warehouse[:i], p.Warehouse[i+1:]...)
			p.appendLogs([]string{fmt.Sprintf("分解仓库装备：%s（金币 +%d，材料 +%d）", name, goldGain, matGain)})
			return goldGain, matGain, nil
		}
	}
	return 0, 0, fmt.Errorf("仓库中没有该装备：%s", itemID)
}

// WarehouseDecomposeAll 批量分解仓库全部装备（v1.9.41）：逐件入账，返回（件数, 金币合计, 材料合计）。
func (p *PlayerState) WarehouseDecomposeAll() (int, int, int) {
	n, gold, mat := 0, 0, 0
	kept := p.Warehouse[:0]
	for _, it := range p.Warehouse {
		if it.Kind == "equip" {
			n++
			g, m := decomposeGoldOf(it.Equip), decomposeMatOf(it.Equip)
			gold += g
			mat += m
			name := it.ID
			if it.Equip != nil {
				name = it.Equip.ID
			}
			p.appendLogs([]string{fmt.Sprintf("分解仓库装备：%s（金币 +%d，材料 +%d）", name, g, m)})
			continue
		}
		kept = append(kept, it)
	}
	p.Warehouse = kept
	p.Core.Gold += gold
	p.Core.Material += mat
	return n, gold, mat
}

// SetDecomposeConfig 注入分解收益基准表（net 层从 economy 配置同步进来，v1.9.41）。
func SetDecomposeConfig(goldBase, matBase []int, enhMult float64) {
	if len(goldBase) > 0 {
		decomposeGoldBase = goldBase
	}
	if len(matBase) > 0 {
		decomposeMatBase = matBase
	}
	if enhMult > 0 {
		decomposeEnhMult = enhMult
	}
}

// decomposeGoldOf / decomposeMatOf 按品质+强化等级算分解收益（与 net 层单件公式同源）。
// 注：金额基准表由 config 注入（economy.drop.decompose），这里用包级变量由 init 配置同步。
var decomposeGoldBase = []int{40, 120, 300, 800, 2000}
var decomposeMatBase = []int{10, 30, 80, 200, 500}
var decomposeEnhMult = 0.15

func decomposeGoldOf(e *Equip) int {
	if e == nil {
		return decomposeGoldBase[0]
	}
	q := e.Quality
	if q < 0 || q >= len(decomposeGoldBase) {
		q = 0
	}
	return int(float64(decomposeGoldBase[q]) * (1 + float64(e.Enhance)*decomposeEnhMult))
}
func decomposeMatOf(e *Equip) int {
	if e == nil {
		return decomposeMatBase[0]
	}
	q := e.Quality
	if q < 0 || q >= len(decomposeMatBase) {
		q = 0
	}
	return decomposeMatBase[q]
}

// WarehouseEquipOut 仓库装备 → 背包（取出一件，未穿戴状态）。
func (p *PlayerState) WarehouseEquipOut(itemID string) error {
	for i := range p.Warehouse {
		if p.Warehouse[i].Kind == "equip" && p.Warehouse[i].ID == itemID {
			e := p.Warehouse[i].Equip
			if e == nil {
				return fmt.Errorf("仓库装备数据异常：%s", itemID)
			}
			e.Equipped = false
			p.Equip = append(p.Equip, *e)
			p.Warehouse = append(p.Warehouse[:i], p.Warehouse[i+1:]...)
			p.appendLogs([]string{fmt.Sprintf("取出装备：%s", itemID)})
			return nil
		}
	}
	return fmt.Errorf("仓库中没有该装备：%s", itemID)
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
