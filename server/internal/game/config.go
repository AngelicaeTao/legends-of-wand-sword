// Package game 承载服务端权威的游戏逻辑：配置加载、属性计算、自动战斗、离线结算。
// 所有数值来自 configs/*.json，本包只跑逻辑，不硬编码数值——改数值只改 JSON。
package game

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Config 是全部游戏配置的聚合根。Init 时从配置目录一次性加载。
type Config struct {
	Classes   ClassesConfig   `json:"-"`
	Skills    SkillsConfig    `json:"-"`
	Equipment EquipmentConfig `json:"-"`
	Monsters  MonstersConfig  `json:"-"`
	Economy   EconomyConfig   `json:"-"`
	Worlds    WorldsConfig    `json:"-"`
	Creator   CreatorConfig   `json:"-"` // 创建/换装部件清单（creator_manifest v2）

	dir string
}

// 原始结构体：字段刻意贴近 JSON，改配置不改结构体即可增减条目。
type ClassesConfig struct {
	BaseStats    map[string]float64 `json:"baseStats"`
	LevelGrowth  map[string]any     `json:"levelGrowth"`
	Classes      []ClassDef         `json:"classes"`
	Transfers    []TransferDef      `json:"transfers"`
	TransferRule map[string]any     `json:"transferRules"`
}
type ClassDef struct {
	ID        string             `json:"id"`
	Name      string             `json:"name"`
	Tier      int                `json:"tier"`
	Icon      string             `json:"icon"`
	Desc      string             `json:"desc"`
	Bonus     map[string]float64 `json:"bonus"`
	SkillPool []string           `json:"skillPool"`
}
type TransferDef struct {
	From string   `json:"from"`
	To   []string `json:"to"`
}

type SkillsConfig struct {
	Slots       int                  `json:"slots"`
	BasicAttack SkillDef             `json:"basicAttack"`
	Skills      []SkillDef           `json:"skills"`
	Classes     map[string]SkillPool `json:"classes"` // 职业技能池（交付包 skills.json §4.3）
}
type SkillDef struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Kind    string  `json:"kind"` // physical/magic/heal/shield/buff/debuff
	Power   float64 `json:"power"`
	CD      float64 `json:"cd"`
	Element string  `json:"element"`
	FX      string  `json:"fx"`
	ClassID string  `json:"classId"` // 归属职业（all=通用）
	Tier    string  `json:"tier"`    // basic/initial/advanced
	Desc    string  `json:"desc"`
	// v1.9.40 持续效果：HotRounds>0 的 heal 为持续回血（施放立即回 hotPer×最大血量，
	// 之后 HotRounds-1 回合每回合再回）；DRRounds>0 的 shield 为持续减伤（DRRounds
	// 回合内受到伤害降低 DRPct，替代一次性护盾值）。
	HotRounds int     `json:"hotRounds,omitempty"`
	HotPer    float64 `json:"hotPer,omitempty"`
	DRRounds  int     `json:"drRounds,omitempty"`
	DRPct     float64 `json:"drPct,omitempty"`
}

// SkillPool 是一个职业的 BD 可选池：初始技能 + 进阶技能。
type SkillPool struct {
	Name      string   `json:"name"`
	Tier      int      `json:"tier"`
	From      string   `json:"from"`
	NeedLevel int      `json:"needLevel"`
	NeedStage int      `json:"needStage"`
	Initial   []string `json:"initial"`
	Advanced  []string `json:"advanced"`
}

// CreatorConfig 对应交付包 creator_manifest.json（format=creator_parts_v2）。
type CreatorConfig struct {
	Format       string                 `json:"format"`
	CharacterSize []int                 `json:"character_size"`
	ScaleRoot    int                    `json:"scaleRoot"`
	OverlayOrder []string               `json:"overlay_order"`
	Slots        map[string]CreatorSlot `json:"slots"`
}
type CreatorSlot struct {
	Label   string          `json:"label"`
	Mode    string          `json:"mode"`
	Options []CreatorOption `json:"options"`
}
type CreatorOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	File string `json:"file"`
	Size []int  `json:"size"`
}

type EquipmentConfig struct {
	Slots            []string                      `json:"slots"`
	SlotNames        map[string]string             `json:"slotNames"`
	Quality          []QualityDef                  `json:"quality"`
	BaseStatsBySlot  map[string]map[string]float64 `json:"baseStatsBySlot"`
	Enhance          map[string]any                `json:"enhance"`
	ClassCoefficient map[string]any                `json:"classCoefficient"`
	WeaponClasses    map[string][]string           `json:"weaponClasses"` // v1.9.38 武器分系：warrior/mage 系各自覆盖的职业
	WeaponNames      map[string][]string           `json:"weaponNames"`   // v1.9.38 两套武器命名（按品质 0-4 取名）
}
type QualityDef struct {
	ID    int     `json:"id"`
	Name  string  `json:"name"`
	Mult  float64 `json:"mult"`
	Color string  `json:"color"`
}

type MonstersConfig struct {
	Monsters  []MonsterDef   `json:"monsters"`
	SpawnRule map[string]any `json:"spawnRule"`
}
type MonsterDef struct {
	ID       string             `json:"id"`
	Name     string             `json:"name"`
	Sprite   string             `json:"sprite"`
	IsBoss   bool               `json:"isBoss"`
	Base     map[string]float64 `json:"base"`
	Growth   float64            `json:"growth"`
	Exp      float64            `json:"exp"`
	Gold     float64            `json:"gold"`
	DropTier int                `json:"dropTier"`
}

type EconomyConfig struct {
	Idle        map[string]any `json:"idle"`
	Stamina     map[string]any `json:"stamina"`
	Battle      map[string]any `json:"battle"`
	Drop        map[string]any `json:"drop"`
	WorldMap    map[string]any `json:"worldMap"`
	Rendering   map[string]any `json:"rendering"`
	Multiplayer map[string]any `json:"multiplayer"`
	Dungeons    []DungeonDef   `json:"dungeons"`
	// v1.9.53 商店兑换比例：material/exp 两段，每段按货币给单价；缺配置时用代码默认值。
	Shop map[string]any `json:"shop"`
}

type DungeonDef struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Level  int    `json:"lv"`
	PerDay int    `json:"perDay"`
	Drop   string `json:"drop"`
	Mon    string `json:"mon"`
	Elite  bool   `json:"elite"`
}
type WorldsConfig struct {
	Worlds  []WorldDef     `json:"worlds"`
	Respawn map[string]any `json:"respawn"`
}
type WorldDef struct {
	ID     string     `json:"id"`
	Name   string     `json:"name"`
	Level  int        `json:"level"`
	Theme  string     `json:"theme"`
	BG     string     `json:"bg"`
	Spawns []SpawnDef `json:"spawns"`
	// v1.9.46 地图产出系数：宝箱经验/金币/钻石/材料/药水按此放大。
	// 配置为 1.0 / 1.2 / 1.44 …（每张图比上一张多 20%，累积），未配置时按 1.0 处理。
	RewardMult float64 `json:"rewardMult"`
}
type SpawnDef struct {
	ID           string `json:"id"`
	Type         string `json:"type"` // MOB/ELITE/CHEST/GATE/ANCHOR/BOSS
	Tier         int    `json:"tier"`
	X            int    `json:"x"`
	Y            int    `json:"y"`
	To           string `json:"to"`
	RequireLevel int    `json:"requireLevel"`
	BossMonster  string `json:"bossMonster"`
	Content      string `json:"content"` // FUNC 点位的功能标识（v1.9.31 家园：warehouse/idle/dress/gate）
	// v1.9.46 BOSS 点位随机落点：坐标只是兜底，客户端在可行走区域随机取点后渲染。
	// 服务端只按 nodeId 判胜负与冷却，不校验坐标，故随机不影响结算权威性。
	RandomPos bool `json:"randomPos"`
}

var (
	globalCfg *Config
	cfgOnce   sync.Once
)

// Init 从目录加载全部配置。dir 为 configs/ 路径（相对或绝对）。
func Init(dir string) error {
	var err error
	cfgOnce.Do(func() {
		c := &Config{dir: dir}
		load := func(name string, out any) error {
			b, e := os.ReadFile(filepath.Join(dir, name))
			if e != nil {
				return fmt.Errorf("read %s: %w", name, e)
			}
			if e := json.Unmarshal(b, out); e != nil {
				return fmt.Errorf("parse %s: %w", name, e)
			}
			return nil
		}
		if e := load("classes.json", &c.Classes); e != nil {
			err = e
			return
		}
		if e := load("skills.json", &c.Skills); e != nil {
			err = e
			return
		}
		if e := load("equipment.json", &c.Equipment); e != nil {
			err = e
			return
		}
		if e := load("monsters.json", &c.Monsters); e != nil {
			err = e
			return
		}
		if e := load("economy.json", &c.Economy); e != nil {
			err = e
			return
		}
		if e := load("worlds.json", &c.Worlds); e != nil {
			err = e
			return
		}
		if e := load("creator.json", &c.Creator); e != nil {
			err = e
			return
		}
		globalCfg = c
	})
	return err
}

// C 返回全局配置（须先 Init）。
func C() *Config {
	if globalCfg == nil {
		panic("game config not initialized; call game.Init(dir) first")
	}
	return globalCfg
}

// Reload 强制重新加载配置（用于配置热更新，灵活修改的关键）。
func Reload(dir string) error {
	globalCfg = nil
	cfgOnce = sync.Once{}
	return Init(dir)
}

func (c *Config) ClassByID(id string) (ClassDef, bool) {
	for _, cl := range c.Classes.Classes {
		if cl.ID == id {
			return cl, true
		}
	}
	return ClassDef{}, false
}
func (c *Config) SkillByID(id string) (SkillDef, bool) {
	for _, s := range c.Skills.Skills {
		if s.ID == id {
			return s, true
		}
	}
	return SkillDef{}, false
}
func (c *Config) MonsterByID(id string) (MonsterDef, bool) {
	for _, m := range c.Monsters.Monsters {
		if m.ID == id {
			return m, true
		}
	}
	return MonsterDef{}, false
}
func (c *Config) WorldByID(id string) (WorldDef, bool) {
	for _, w := range c.Worlds.Worlds {
		if w.ID == id {
			return w, true
		}
	}
	return WorldDef{}, false
}

// SkillPoolOf 返回职业的 BD 可选池（initial ∪ advanced）。未知职业返回空池。
func (c *Config) SkillPoolOf(classID string) SkillPool {
	if c.Skills.Classes == nil {
		return SkillPool{}
	}
	return c.Skills.Classes[classID]
}

// BDPoolOf 返回职业全部可用技能 id（初始 + 进阶，保持声明顺序）。
func (c *Config) BDPoolOf(classID string) []string {
	p := c.SkillPoolOf(classID)
	out := make([]string, 0, len(p.Initial)+len(p.Advanced))
	out = append(out, p.Initial...)
	out = append(out, p.Advanced...)
	return out
}

// InBDPool 判断技能是否属于该职业的技能池（交付包规则：不显示/不允许跨职业技能）。
func (c *Config) InBDPool(classID, skillID string) bool {
	if skillID == "" || skillID == "basic" {
		return true
	}
	for _, id := range c.BDPoolOf(classID) {
		if id == skillID {
			return true
		}
	}
	return false
}

// LookSlotValid 校验 homeLook 的一个槽位取值是否合法（帽子允许 "none"=不戴帽）。
func (c *Config) LookSlotValid(slot, id string) bool {
	if slot == "hat" && id == "none" {
		return true
	}
	sl, ok := c.Creator.Slots[slot]
	if !ok {
		return false
	}
	for _, o := range sl.Options {
		if o.ID == id {
			return true
		}
	}
	return false
}

// DefaultLook 返回一套合法默认家园外观（角色创建异常兜底 / 老存档迁移用）。
func (c *Config) DefaultLook() map[string]string {
	out := map[string]string{}
	for slot, sl := range c.Creator.Slots {
		if len(sl.Options) > 0 {
			out[slot] = sl.Options[0].ID
		}
	}
	return out
}
