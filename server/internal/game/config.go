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
	Slots       int        `json:"slots"`
	BasicAttack SkillDef   `json:"basicAttack"`
	Skills      []SkillDef `json:"skills"`
}
type SkillDef struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Kind    string  `json:"kind"` // physical/magic/heal/shield/buff/debuff
	Power   float64 `json:"power"`
	CD      float64 `json:"cd"`
	Element string  `json:"element"`
	FX      string  `json:"fx"`
}

type EquipmentConfig struct {
	Slots            []string                      `json:"slots"`
	Quality          []QualityDef                  `json:"quality"`
	BaseStatsBySlot  map[string]map[string]float64 `json:"baseStatsBySlot"`
	Enhance          map[string]any                `json:"enhance"`
	ClassCoefficient map[string]any                `json:"classCoefficient"`
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
