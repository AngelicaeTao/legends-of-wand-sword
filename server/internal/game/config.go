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
	Slots        int                  `json:"slots"`
	PassiveSlots int                  `json:"passiveSlots"` // v1.9.89 转职后可装配的被动槽位数
	BasicAttack  SkillDef             `json:"basicAttack"`
	Skills       []SkillDef           `json:"skills"`
	Classes      map[string]SkillPool `json:"classes"`   // 职业技能池（交付包 skills.json §4.3）
	PowerRule    SkillPowerRule       `json:"powerRule"` // v1.9.85 技能/技能等级计入战力
}

// SkillPowerRule 技能战力规则（v1.9.85）：
// 已装备技能每个贡献 base[档位] × kindWeight[类型] × SkillEffectMult(等级)，累加进总战力。
// 等级系数复用 economy.skillUpgrade.perLevel，不再单独配置，避免两处不一致。
type SkillPowerRule struct {
	Base       map[string]float64 `json:"base"`       // 档位（initial/advanced）→ 基础分
	KindWeight map[string]float64 `json:"kindWeight"` // 技能类型权重（输出类 1.0，辅助类 <1）
}
type SkillDef struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Kind    string  `json:"kind"` // physical/magic/heal/shield/buff/debuff/summon
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
	// v1.9.82 术士专属：Lifesteal>0 的伤害技能命中后回复本次伤害的该比例（吸取 50%）；
	// Kind=summon 的技能施放时不造成伤害，而是召唤召唤物协战 SummonRounds 回合，
	// 之后每个英雄回合额外造成 SummonPower×攻击 的暗系伤害（受技能等级加成）。
	Lifesteal    float64 `json:"lifesteal,omitempty"`
	SummonRounds int     `json:"summonRounds,omitempty"`
	SummonPower  float64 `json:"summonPower,omitempty"`
	// v1.9.87 debuff 拆成两件事：Power = 伤害系数（0 表示纯减益不造成伤害），
	// AtkDown = 命中后敌人攻击力降低的比例（本场持续，覆盖上一次）。此前 Power 一字段两义
	// （既当伤害又当降攻），导致单人把它当降攻、组队把它当伤害，两端行为不一致。
	AtkDown float64 `json:"atkDown,omitempty"`

	// —— v1.9.89 被动技能（Kind=passive）——
	// 被动不进 BD 槽位，单独占 2 个被动槽（转职后解锁）；效果分两类：
	// ① 属性型：HPPct/ATKPct/DEFPct/CritAdd/SpdAdd，在 CalcStats 里按乘/加生效；
	// ② 战斗特效型：由 passive.go 汇总成 PassiveBonus，单人 battle.go 与组队
	//    party_battle.go 共用同一套钩子，保证两端行为一致。
	// 全部字段可缺省（零值不生效），一个被动可同时带多种效果。
	HPPct        float64            `json:"hpPct,omitempty"`        // 最大生命百分比（可为负）
	ATKPct       float64            `json:"atkPct,omitempty"`       // 攻击百分比（可为负）
	DEFPct       float64            `json:"defPct,omitempty"`       // 防御百分比（可为负）
	CritAdd      float64            `json:"critAdd,omitempty"`      // 暴击率加值（绝对值，0.05 = +5%）
	SpdAdd       float64            `json:"spdAdd,omitempty"`       // 速度加值（绝对值）
	LifestealAdd float64            `json:"lifestealAdd,omitempty"` // 所有伤害附带吸血比例
	DRAdd        float64            `json:"drAdd,omitempty"`        // 常驻受伤减免比例
	Thorns       float64            `json:"thorns,omitempty"`       // 受击反弹伤害比例（按受到的伤害计）
	OpenShield   float64            `json:"openShield,omitempty"`   // 战斗开始获得护盾（×攻击力）
	HealBoost    float64            `json:"healBoost,omitempty"`    // 治疗类技能回复量增幅
	ShieldBoost  float64            `json:"shieldBoost,omitempty"`  // 护盾类技能效果增幅
	CDR          float64            `json:"cdr,omitempty"`          // 全技能冷却缩减比例（上限 0.5）
	LowHPThresh  float64            `json:"lowHpThresh,omitempty"`  // 低血增伤触发阈值（生命占比）
	LowHPDmg     float64            `json:"lowHpDmg,omitempty"`     // 低于阈值时的增伤比例
	ExtraEvery   int                `json:"extraEvery,omitempty"`   // 每 N 个英雄回合追加一次攻击
	ExtraPower   float64            `json:"extraPower,omitempty"`   // 追加攻击的伤害系数（×攻击）
	RegenEvery   int                `json:"regenEvery,omitempty"`   // 每 N 个英雄回合回复一次
	RegenPct     float64            `json:"regenPct,omitempty"`     // 每次回复的最大生命占比
	KindDmg      map[string]float64 `json:"kindDmg,omitempty"`      // 按技能类型增伤（physical/magic/debuff…）
	DebuffBoost  float64            `json:"debuffBoost,omitempty"`  // 降攻类减益的效果增幅
}

// SkillPool 是一个职业的 BD 可选池：初始技能 + 进阶技能 + 被动技能。
type SkillPool struct {
	Name      string   `json:"name"`
	Tier      int      `json:"tier"`
	From      string   `json:"from"`
	NeedLevel int      `json:"needLevel"`
	NeedStage int      `json:"needStage"`
	Initial   []string `json:"initial"`
	Advanced  []string `json:"advanced"`
	// v1.9.89 被动池：转职职业各 4 个，一阶职业为空。转职即全部获得（无需解锁），
	// 但只有 2 个被动槽，需要玩家自己取舍。
	Passives []string `json:"passives"`
}

// CreatorConfig 对应交付包 creator_manifest.json（format=creator_parts_v2）。
type CreatorConfig struct {
	Format        string                 `json:"format"`
	CharacterSize []int                  `json:"character_size"`
	ScaleRoot     int                    `json:"scaleRoot"`
	OverlayOrder  []string               `json:"overlay_order"`
	Slots         map[string]CreatorSlot `json:"slots"`
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
	// v1.9.53 图域首领动态强度系数（血/攻按玩家装备品质、强化、推进进度上调）
	BossScaling map[string]any `json:"bossScaling"`
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
	// v1.9.53 按挑战者成长动态放大血/攻（仅大地图首领开启，副本首领保持固定）
	ScaleWithPlayer bool `json:"scaleWithPlayer"`
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
	// v1.9.56 增加 sell 段：背包材料/药水回收单价（按物品 id 定价，未列出按类别兜底）。
	Shop map[string]any `json:"shop"`
	// v1.9.56 技能升级：每级效果 +8%，升级消耗钻石随等级递增；缺配置时用代码默认值。
	SkillUpgrade map[string]any `json:"skillUpgrade"`
}

type DungeonDef struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Level  int    `json:"lv"`
	PerDay int    `json:"perDay"`
	Drop   string `json:"drop"`
	Mon    string `json:"mon"`
	Elite  bool   `json:"elite"`
	// v1.9.68 组队副本：推荐战力 + 队伍上限 + BOSS 血量倍率
	RecPower   int     `json:"recPower"`
	PartyMax   int     `json:"partyMax"`
	BossHpMult float64 `json:"bossHpMult"`
	// v1.9.77 车轮战：按波次配置怪物阵容（3 小怪 / 2 精英 / 1 首领），血量跨波不重置
	Waves []WaveSpec `json:"waves"`
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
	WorldW int        `json:"worldW,omitempty"` // v2.0 每世界尺寸（缺省 7680×4320）
	WorldH int        `json:"worldH,omitempty"`
	Paint  *bool      `json:"paint,omitempty"` // false=地形已完整绘制，客户端跳过补绘
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
	// v2.0 世界地图：BOSS 独立等级、击杀解锁世界、GATE 是否要求目标世界已解锁
	Level           int    `json:"level"`
	Unlock          string `json:"unlock"`
	RequireUnlocked bool   `json:"requireUnlocked"`
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

// PassiveSlotMax 转职后可装配的被动槽位数（v1.9.89），读 skills.json 的 passiveSlots。
// 一阶职业（战士/法师）被动池为空，槽位对它们不生效，前端据此决定是否显示被动区。
func PassiveSlotMax() int {
	if n := C().Skills.PassiveSlots; n > 0 {
		return n
	}
	return 2 // 配置缺省时的兜底，与 skills.json 默认值一致
}

// PassivePool 返回某职业的被动池（v1.9.89）。未配置/一阶职业返回空切片。
func (c *Config) PassivePool(classID string) []string {
	p, ok := c.Skills.Classes[classID]
	if !ok || len(p.Passives) == 0 {
		return []string{}
	}
	return p.Passives
}

// IsPassive 判断某技能 id 是否被动技能（v1.9.89）。
// 装配校验用它拦掉「把主动技能塞进被动槽」这类脏数据。
func (c *Config) IsPassive(id string) bool {
	s, ok := c.SkillByID(id)
	return ok && s.Kind == "passive"
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

// InPassivePool 判断技能是否属于该职业的被动池（v1.9.89）。
// 被动不走解锁流程（转职即全部获得），装配时只需校验「是当前职业的被动」。
func (c *Config) InPassivePool(classID, skillID string) bool {
	if skillID == "" {
		return false
	}
	for _, id := range c.PassivePool(classID) {
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
