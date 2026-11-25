// Package store 负责角色存档的分表结构定义与序列化。
// 对应 Nakama Storage（逻辑分表）+ 可选关系库；新增系统走 Ext 扩展位，不改表结构。
package store

// PlayerState 是角色完整存档的内存表示。每个字段对应一个逻辑表。
type PlayerState struct {
	UserID    string         `json:"userId"`
	Core      Core           `json:"core"`      // characters
	World     World          `json:"world"`     // world_progress
	Warehouse []Item         `json:"warehouse"` // warehouse
	Equip     []Equip        `json:"equip"`     // equipment
	Skills    Skills         `json:"skills"`    // skills
	Pets      Pets           `json:"pets"`      // pets / pet_state（预留）
	Home      Home           `json:"home"`      // home（预留）
	Logs      []LogEntry     `json:"logs"`      // logs
	Ext       map[string]any `json:"ext"`       // ext_data 扩展位
}

type Core struct {
	Name        string `json:"name"`
	Level       int    `json:"level"`
	Exp         int    `json:"exp"`
	BattlePower int    `json:"battlePower"`
	ClassID     string `json:"classId"`
	Gold        int    `json:"gold"`
	Gem         int    `json:"gem"`
	Material    int    `json:"material"`
	Stamina     int    `json:"stamina"`
}
type World struct {
	WorldID  string   `json:"worldId"`
	X        int      `json:"x"`
	Y        int      `json:"y"`
	Unlocked []string `json:"unlocked"`
	LastSeen int64    `json:"lastSeen"`
}
type Item struct {
	ID   string `json:"id"`
	Qty  int    `json:"qty"`
	Slot int    `json:"slot"`
	Kind string `json:"kind"`
}
type Equip struct {
	ID       string             `json:"id"`
	Slot     string             `json:"slot"`
	Quality  int                `json:"quality"`
	Enhance  int                `json:"enhance"`
	Stats    map[string]float64 `json:"stats"`
	Equipped bool               `json:"equipped"`
}
type Skills struct {
	Slots    []string `json:"slots"` // 4个已装备技能
	Unlocked []string `json:"unlocked"`
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
func NewPlayer(userID, name, classID string) *PlayerState {
	if classID == "" {
		classID = "warrior"
	}
	return &PlayerState{
		UserID:    userID,
		Core:      Core{Name: name, Level: 1, ClassID: classID, Gold: 3000, Gem: 900, Material: 20, Stamina: 86},
		World:     World{WorldID: "W1", X: 7680, Y: 4320, Unlocked: []string{"W1"}, LastSeen: 0},
		// 初始仓库：12 占用（交付包占位口径），含材料与 4 件基础装备
		Warehouse: []Item{
			{ID: "钢锭", Qty: 24, Kind: "材料"}, {ID: "精铁", Qty: 8, Kind: "材料"},
			{ID: "蓝宝石", Qty: 3, Kind: "材料"}, {ID: "药水", Qty: 12, Kind: "消耗品"},
			{ID: "皮革", Qty: 18, Kind: "材料"}, {ID: "布料", Qty: 9, Kind: "材料"},
		},
		// 初始装备：武器已穿戴，其余在仓库（Equipped=false）供体验拆装
		Equip: []Equip{
			{ID: "练习剑", Slot: "weapon", Quality: 0, Enhance: 0, Equipped: true, Stats: map[string]float64{"atk": 10}},
			{ID: "布帽", Slot: "helmet", Quality: 0, Enhance: 0, Equipped: false, Stats: map[string]float64{"hp": 60, "def": 3}},
			{ID: "布袍", Slot: "armor", Quality: 0, Enhance: 0, Equipped: false, Stats: map[string]float64{"hp": 100, "def": 6}},
			{ID: "铜戒", Slot: "trinket", Quality: 0, Enhance: 0, Equipped: false, Stats: map[string]float64{"atk": 4}},
		},
		Skills: Skills{Slots: []string{"slash"}, Unlocked: []string{"slash"}},
		Ext:    map[string]any{"bag": map[string]any{"金币袋": float64(5), "秘银": float64(2)}},
		Logs:   []LogEntry{},
	}
}

// TableNames 列出全部逻辑表，供数据库面板/导出展示。
// 新增系统时：往 Ext 写入无需改这里；正式建表则追加一行 + database schema 迁移。
var TableNames = []string{
	"characters", "world_progress", "warehouse", "equipment",
	"skills", "pets", "pet_state", "home", "logs", "ext_data", "schema_meta",
}
