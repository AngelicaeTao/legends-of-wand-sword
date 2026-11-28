// Package store 负责角色存档的分表结构定义与序列化。
// 对应 Nakama Storage（逻辑分表）+ 可选关系库；新增系统走 Ext 扩展位，不改表结构。
package store

import (
	"strings"

	"zjcs/server/internal/game"
)

// PlayerState 是角色完整存档的内存表示。每个字段对应一个逻辑表。
type PlayerState struct {
	UserID     string         `json:"userId"`
	Core       Core           `json:"core"`            // characters
	World      World          `json:"world"`           // world_progress
	Warehouse  []Item         `json:"warehouse"`       // warehouse
	Equip      []Equip        `json:"equip"`           // equipment
	Skills     Skills         `json:"skills"`          // skills
	Pets       Pets           `json:"pets"`            // pets / pet_state（预留）
	Home       Home           `json:"home"`            // home（预留）
	Appearance Appearance     `json:"appearance"`      // appearance（v1.9.7 角色创建与换装）
	Guild      *GuildRef      `json:"guild,omitempty"` // 公会引用（nil=未加入，v1.9.18）
	Logs       []LogEntry     `json:"logs"`            // logs
	Ext        map[string]any `json:"ext"`             // ext_data 扩展位
}

// Appearance 外观表（交付包 §3）：created=false 时强制进创建界面；
// homeLook 只存选项 id（face_01/hair_03…），不存图片路径，换素材不用改存档。
type Appearance struct {
	Created  bool              `json:"created"`
	Face     string            `json:"face"`     // 永久脸型 id（=homeLook.face 冗余）
	HomeLook map[string]string `json:"homeLook"` // {face,hair,top,pants,shoes,hat}
}

type Core struct {
	Name        string  `json:"name"`
	Level       int     `json:"level"`
	Exp         int     `json:"exp"`
	BattlePower int     `json:"battlePower"`
	ClassID     string  `json:"classId"`
	Gold        int     `json:"gold"`
	Gem         int     `json:"gem"`
	Material    int     `json:"material"`
	Stamina     int     `json:"stamina"`
	HP          float64 `json:"hp"` // v1.9.27 全局当前血量：战斗后保留余量不回满；0/缺省=满血（老档兼容，加载时回填）
}
type World struct {
	WorldID     string   `json:"worldId"`
	X           int      `json:"x"`
	Y           int      `json:"y"`
	Unlocked    []string `json:"unlocked"`
	LastSeen    int64    `json:"lastSeen"`
	LastWorldID string   `json:"lastWorldId,omitempty"` // v1.9.31 进入家园前的世界（战败回家园 / 出门返回用）
}
type Item struct {
	ID    string `json:"id"`
	Qty   int    `json:"qty"`
	Slot  int    `json:"slot"`
	Kind  string `json:"kind"`            // material / consume / equip
	Equip *Equip `json:"equip,omitempty"` // Kind=equip 时的完整装备数据（品质/强化/属性）
}
type Equip struct {
	ID       string             `json:"id"`
	Slot     string             `json:"slot"`
	Quality  int                `json:"quality"`
	Enhance  int                `json:"enhance"`
	Stats    map[string]float64 `json:"stats"`
	Equipped bool               `json:"equipped"`
	ClassID  string             `json:"classId,omitempty"` // v1.9.38 武器系标记（warrior/mage），空=通用
}
type Skills struct {
	Slots    []string `json:"slots"` // 4个已装备技能
	Unlocked []string `json:"unlocked"`
	// v1.9.56 技能等级（技能id→等级，1 级起，缺省视为 1）。老存档没有该字段时按 1 级处理。
	Levels map[string]int `json:"levels,omitempty"`
	// v1.9.89 已装配的被动技能（最多 game.PassiveSlotMax 个）。
	// 被动不需要解锁：转职后职业池里的 4 个被动直接全部可用，玩家只挑 2 个装上。
	Passives []string `json:"passives,omitempty"`
}
type Pets struct {
	Owned  []string `json:"owned"`
	Active string   `json:"active"`
}
type Home struct {
	Cart int `json:"cart"`
	Bed  int `json:"bed"`
}
type LogEntry struct {
	TS     int64  `json:"ts"`
	Event  string `json:"event"`
	Detail string `json:"detail"`
}

// NewPlayer 创建初始存档。classId 取配置首职业（战士）。
// v1.9.7：新档 Appearance.Created=false —— 首次进入必须走「角色创建」（交付包 §4.1）。
func NewPlayer(userID, name, classID string) *PlayerState {
	if classID == "" {
		classID = "warrior"
	}
	name = SanitizeName(name)
	pool := game.C().BDPoolOf(classID)
	if len(pool) == 0 {
		pool = []string{"w_slash", "w_guard"}
	}
	if len(pool) > 4 {
		pool = pool[:4]
	}
	return &PlayerState{
		UserID: userID,
		Core:   Core{Name: name, Level: 1, ClassID: classID, Gold: 3000, Gem: 900, Material: 20, Stamina: 86},
		World:  World{WorldID: "W1", X: 1680, Y: 3240, Unlocked: []string{"W1"}, LastSeen: 0},
		// 初始仓库：12 占用（交付包占位口径），含材料与 4 件基础装备
		Warehouse: []Item{
			{ID: "钢锭", Qty: 24, Kind: "材料"}, {ID: "精铁", Qty: 8, Kind: "材料"},
			{ID: "蓝宝石", Qty: 3, Kind: "材料"}, {ID: "potion_s", Qty: 12, Kind: "consume"},
			{ID: "皮革", Qty: 18, Kind: "材料"}, {ID: "布料", Qty: 9, Kind: "材料"},
		},
		// 初始装备：武器已穿戴，其余在仓库（Equipped=false）供体验拆装
		// v1.9.38 武器分系：按创角职业发放对应系武器（战士系=练习剑 / 法师系=学徒法杖）
		Equip: func() []Equip {
			wc := game.WeaponClassFor(classID)
			if wc == "mage" {
				return []Equip{
					{ID: "学徒法杖", Slot: "weapon", Quality: 0, Enhance: 0, Equipped: true, Stats: map[string]float64{"atk": 10}, ClassID: "mage"},
					{ID: "布帽", Slot: "helmet", Quality: 0, Enhance: 0, Equipped: false, Stats: map[string]float64{"hp": 60, "def": 3}},
					{ID: "布袍", Slot: "armor", Quality: 0, Enhance: 0, Equipped: false, Stats: map[string]float64{"hp": 100, "def": 6}},
					{ID: "铜戒", Slot: "trinket", Quality: 0, Enhance: 0, Equipped: false, Stats: map[string]float64{"atk": 4}},
				}
			}
			return []Equip{
				{ID: "练习剑", Slot: "weapon", Quality: 0, Enhance: 0, Equipped: true, Stats: map[string]float64{"atk": 10}, ClassID: "warrior"},
				{ID: "布帽", Slot: "helmet", Quality: 0, Enhance: 0, Equipped: false, Stats: map[string]float64{"hp": 60, "def": 3}},
				{ID: "布袍", Slot: "armor", Quality: 0, Enhance: 0, Equipped: false, Stats: map[string]float64{"hp": 100, "def": 6}},
				{ID: "铜戒", Slot: "trinket", Quality: 0, Enhance: 0, Equipped: false, Stats: map[string]float64{"atk": 4}},
			}
		}(),
		Skills:     Skills{Slots: append([]string{}, pool...), Unlocked: append([]string{}, pool...)},
		Appearance: Appearance{Created: false, HomeLook: map[string]string{}},
		Ext:        map[string]any{"newCharacter": true, "bag": map[string]any{"金币袋": float64(5), "秘银": float64(2)}},
		Logs:       []LogEntry{},
	}
}

// MigrateWeaponClass 老档武器系归一（v1.9.38）：存量武器 ClassID 为空时，
// 按玩家当前职业归入对应武器系，保证"武器不互通"对老玩家平滑生效。
// 返回是否发生变更（调用方据此决定是否落盘）。
func (p *PlayerState) MigrateWeaponClass() bool {
	if p.Core.ClassID == "" {
		return false
	}
	wc := game.WeaponClassFor(p.Core.ClassID)
	changed := false
	for i := range p.Equip {
		if p.Equip[i].Slot == "weapon" && p.Equip[i].ClassID == "" {
			p.Equip[i].ClassID = wc
			changed = true
		}
	}
	return changed
}

// TableNames 列出全部逻辑表，供数据库面板/导出展示。
// 新增系统时：往 Ext 写入无需改这里；正式建表则追加一行 + database schema 迁移。
var TableNames = []string{
	"characters", "world_progress", "warehouse", "equipment",
	"skills", "pets", "pet_state", "home", "logs", "ext_data", "schema_meta",
}

// SanitizeName 清洗玩家名 / 公会名：去掉 ASCII 控制字符与 HTML/路径敏感字符，
// TrimSpace 后最多保留 12 个 rune。用于所有接受用户昵称的入口，防注入与脏名字。
func SanitizeName(s string) string {
	cleaned := strings.Map(func(r rune) rune {
		switch r {
		case '<', '>', '&', '"', '\'', '`', '/', '\\':
			return -1
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	cleaned = strings.TrimSpace(cleaned)
	if r := []rune(cleaned); len(r) > 12 {
		cleaned = string(r[:12])
	}
	return cleaned
}
