// 角色创建与换装 · 存档侧权威逻辑（对应《角色创建与换装-完整交付包》§3/§4/§6）
// 规则：图片路径由选项 id 查 creator.json 得到，存档只存 id；服务端校验职业合法性、
// 外观选项合法性、技能合法性，客户端只做表现。
package store

import (
	"fmt"
	"strings"

	"zjcs/server/internal/game"
)

// bdMaxSlots BD 装配槽位上限（与 §6 submit_bd 校验一致）
const bdMaxSlots = 4

// lookSlots 与 creator_manifest.json 的 overlay_order 一致（face→hair→hat→top→pants→shoes）。
var lookSlots = []string{"face", "hair", "top", "pants", "shoes", "hat"}

// LegacySkillAlias 旧技能 id → 新技能 id（v1.9.7 技能表按交付包 17 技能重写后的存档迁移表）。
var LegacySkillAlias = map[string]string{
	"slash":       "w_slash",
	"guard":       "w_guard",
	"shield_bash": "k_holy_strike",
	"whirlwind":   "f_combo",
	"rage":        "f_berserk",
	"execute":     "f_iron_body",
	"fireball":    "m_fireball",
	"heal":        "m_heal",
	"frost_nova":  "s_purify",
	"arcane_bolt": "s_bless",
	"summon":      "wl_summon",
	"meteor":      "wl_drain",
	"curse":       "wl_curse",
	"holy_guard":  "k_aegis",
	"taunt":       "k_taunt",
}

func hasStr(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// NormalizeLook 校验并补全 homeLook（缺省槽位用配置默认项补，非法值直接拒绝）。
func NormalizeLook(in map[string]string) (map[string]string, error) {
	c := game.C()
	def := c.DefaultLook()
	out := map[string]string{}
	for _, slot := range lookSlots {
		id := ""
		if in != nil {
			id = strings.TrimSpace(in[slot])
		}
		if id == "" {
			id = def[slot]
		}
		if id == "" {
			return nil, fmt.Errorf("外观部件缺失：%s", slot)
		}
		if !c.LookSlotValid(slot, id) {
			return nil, fmt.Errorf("外观选项非法：%s=%s", slot, id)
		}
		out[slot] = id
	}
	return out, nil
}

// CreateCharacter 首次创角（服务端权威）：校验职业/外观/技能，写 appearance 并置 created=true。
// 已创建过的存档再次调用会被拒绝（断线重连不会重复创建）。
func (p *PlayerState) CreateCharacter(classID string, look map[string]string, name string, skills []string) error {
	c := game.C()
	pool, ok := c.Skills.Classes[classID]
	if !ok {
		return fmt.Errorf("职业不存在：%s", classID)
	}
	if pool.Tier != 0 {
		return fmt.Errorf("开局只能选择战士或法师")
	}
	if p.Appearance.Created {
		return fmt.Errorf("角色已创建，不能重复创建")
	}
	norm, err := NormalizeLook(look)
	if err != nil {
		return err
	}
	unlocked := append([]string{}, pool.Initial...)
	slots := append([]string{}, pool.Initial...)
	if len(skills) > 0 {
		slots = slots[:0]
		for _, sid := range skills {
			if !hasStr(unlocked, sid) {
				return fmt.Errorf("初始技能非法：%s", sid)
			}
			if !hasStr(slots, sid) {
				slots = append(slots, sid)
			}
		}
		if len(slots) == 0 {
			slots = append([]string{}, pool.Initial...)
		}
	}
	p.Core.ClassID = classID
	if n := strings.TrimSpace(name); n != "" {
		if len([]rune(n)) > 12 {
			n = string([]rune(n)[:12])
		}
		p.Core.Name = n
	}
	p.Skills.Unlocked = unlocked
	p.Skills.Slots = slots
	p.Appearance = Appearance{Created: true, Face: norm["face"], HomeLook: norm}
	if p.Ext == nil {
		p.Ext = map[string]any{}
	}
	delete(p.Ext, "newCharacter")
	p.appendLogs([]string{fmt.Sprintf("创建角色：%s · %s", p.Core.Name, pool.Name)})
	return nil
}

// UpdateLook 家园换装：只改外观，不动职业/装备/技能（P3 转职不丢脸的反向保证）。
func (p *PlayerState) UpdateLook(look map[string]string) (map[string]string, error) {
	if !p.Appearance.Created {
		return nil, fmt.Errorf("尚未创建角色")
	}
	norm, err := NormalizeLook(look)
	if err != nil {
		return nil, err
	}
	p.Appearance.HomeLook = norm
	p.Appearance.Face = norm["face"]
	return norm, nil
}

// TransferTo 转职 / 进阶：校验等级与阶段门槛，重算技能池（转职后原职业技能自动卸下）。
// stage = 已开启的世界数（need_stage 语义按交付包 [PLACEHOLDER]，此处以"已解锁世界数"落地）。
func (p *PlayerState) TransferTo(to string, stage int) error {
	c := game.C()
	pool, ok := c.Skills.Classes[to]
	if !ok {
		return fmt.Errorf("职业不存在：%s", to)
	}
	if pool.Tier > 0 {
		need := pool.NeedLevel
		if need <= 0 {
			need = 44
		}
		if p.Core.Level < need {
			return fmt.Errorf("等级不足：进阶 %s 需 Lv.%d，当前 Lv.%d（先练级/挂机）", pool.Name, need, p.Core.Level)
		}
		needStage := pool.NeedStage
		if needStage <= 0 {
			needStage = 2
		}
		if stage < needStage {
			return fmt.Errorf("进度不足：进阶 %s 需开启第 %d 世界，当前仅 %d 个世界", pool.Name, needStage, stage)
		}
		if pool.From != "" && p.Core.ClassID != pool.From {
			cur, ok2 := c.Skills.Classes[p.Core.ClassID]
			if !ok2 || cur.Tier != 1 || cur.From != pool.From {
				return fmt.Errorf("转职路径非法：%s 不能直接转为 %s", p.Core.ClassID, pool.Name)
			}
		}
	} else if p.Core.ClassID != to {
		// v1.9.26 目标为基础职业：
		//  1) 基础职业之间可互转（战士↔法师，零成本，技能池自动换）；
		//  2) 进阶职业仅限退回其对应的基础职业（如 骑士→战士）；
		//  3) 其余（跨分支回退、伪造目标）一律拒绝。
		cur, ok2 := c.Skills.Classes[p.Core.ClassID]
		if !ok2 || !(cur.Tier == 0 || (cur.Tier == 1 && cur.From == to)) {
			return fmt.Errorf("不能转为基础职业：%s（基础职业可互转；进阶职业仅可退回其对应基础职业）", to)
		}
	}
	p.Core.ClassID = to
	all := c.BDPoolOf(to)
	p.Skills.Unlocked = append([]string{}, all...)
	kept := []string{}
	for _, sid := range p.Skills.Slots {
		if hasStr(all, sid) && !hasStr(kept, sid) {
			kept = append(kept, sid)
		}
	}
	if len(kept) == 0 {
		kept = append([]string{}, pool.Initial...)
	}
	p.Skills.Slots = kept
	return nil
}

// Migrate 存档迁移（v1.9.7）：
//  1. 旧技能 id → 新技能 id，并剔除跨职业技能（交付包 §4.3）；
//  2. 老存档补 appearance（老玩家不该被强制重新创建角色）；
//  3. 装备装外观由 classId 决定，无需迁移。
//
// 返回是否发生了改动（调用方据此决定是否回写磁盘）。
func (p *PlayerState) Migrate() bool {
	c := game.C()
	changed := false
	fix := func(id string) string {
		if _, ok := c.SkillByID(id); ok {
			return id
		}
		if n, ok := LegacySkillAlias[id]; ok {
			return n
		}
		return ""
	}
	pool := c.BDPoolOf(p.Core.ClassID)
	if len(pool) == 0 {
		p.Core.ClassID = "warrior"
		pool = c.BDPoolOf("warrior")
		changed = true
	}
	inPool := func(id string) bool { return hasStr(pool, id) }

	un := []string{}
	for _, id := range p.Skills.Unlocked {
		if n := fix(id); n != "" && inPool(n) && !hasStr(un, n) {
			un = append(un, n)
		}
	}
	if len(un) != len(p.Skills.Unlocked) || !sameSet(un, p.Skills.Unlocked) {
		changed = true
	}
	// 基础职业必须至少有初始技能
	ip := c.SkillPoolOf(p.Core.ClassID)
	for _, id := range ip.Initial {
		if !hasStr(un, id) {
			un = append(un, id)
			changed = true
		}
	}
	p.Skills.Unlocked = un

	sl := []string{}
	for _, id := range p.Skills.Slots {
		if n := fix(id); n != "" && inPool(n) && !hasStr(sl, n) {
			sl = append(sl, n)
		}
	}
	if len(sl) == 0 {
		sl = append([]string{}, ip.Initial...)
		changed = true
	}
	// 转职/技能表重写后可能有槽位被清空（跨职业技能卸下），用已解锁的本职业技能补满，
	// 避免玩家上线后 BD 只剩 1 个技能却不知道原因。
	if len(sl) < bdMaxSlots {
		for _, id := range un {
			if len(sl) >= bdMaxSlots {
				break
			}
			if !hasStr(sl, id) {
				sl = append(sl, id)
				changed = true
			}
		}
	}
	if !sameSet(sl, p.Skills.Slots) {
		changed = true
	}
	p.Skills.Slots = sl

	// 外观兜底：老存档（非"待创建"新档）自动视为已创建
	if p.Ext != nil {
		if v, ok := p.Ext["newCharacter"].(bool); ok && v {
			if p.Appearance.Created || len(p.Appearance.HomeLook) > 0 {
				// 新档已被创建流程写过，标记可清理
				delete(p.Ext, "newCharacter")
				changed = true
			}
			return changed
		}
	}
	if !p.Appearance.Created {
		look := c.DefaultLook()
		p.Appearance = Appearance{Created: true, Face: look["face"], HomeLook: look}
		changed = true
	} else if len(p.Appearance.HomeLook) == 0 {
		look := c.DefaultLook()
		p.Appearance.HomeLook = look
		p.Appearance.Face = look["face"]
		changed = true
	}
	return changed
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for _, x := range a {
		if !hasStr(b, x) {
			return false
		}
	}
	return true
}
