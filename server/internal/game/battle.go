package game

import (
	"math"
	"math/rand"
	"sort"
)

// BattleRequest 客户端发起交战请求（CD→S engage）。战斗决策在战前 BD，战斗中零输入。
type BattleRequest struct {
	UserID    string   `json:"userId"`
	Avatar    Avatar   `json:"avatar"`
	BD        []string `json:"bd"` // 战前配置的4个技能
	MonsterID string   `json:"monsterId"`
	MonsterLv int      `json:"monsterLv"`
	Elite     string   `json:"elite"` // ""/elite/rareElite
	Seed      int64    `json:"seed"`  // >0 时用于复现（playtest 回放）；=0 由服务端随机生成
}

// BattleRound 是战报中的一回合（表现用最小单元）。
type BattleRound struct {
	N         int     `json:"n"`
	Actor     string  `json:"actor"`  // hero/monster
	Action    string  `json:"action"` // 技能id或basic
	SkillName string  `json:"skillName"`
	FX        string  `json:"fx"`
	Damage    float64 `json:"damage"`
	Heal      float64 `json:"heal"`
	HeroHP    float64 `json:"heroHp"`
	MonHP     float64 `json:"monHp"`
}

// BattleReport 服务端下发的权威战报。客户端只做表现，不参与结算。
type BattleReport struct {
	Seed    int64         `json:"seed"`
	Result  string        `json:"result"` // win/lose
	Rounds  []BattleRound `json:"rounds"`
	Exp     int           `json:"exp"`
	Gold    int           `json:"gold"`
	Drops   []EquipItem   `json:"drops"`
	Seconds float64       `json:"seconds"`
	Log     string        `json:"log"`
}

// ResolveBattle 服务端权威结算。给定相同 seed + 输入必得相同战报（可复现 bug / playtest 回放）。
func ResolveBattle(req BattleRequest) BattleReport {
	c := C()
	seed := req.Seed
	if seed <= 0 {
		seed = rand.Int63()
	}
	rng := rand.New(rand.NewSource(seed))
	hero := CalcStats(req.Avatar)

	mon, ok := c.MonsterByID(req.MonsterID)
	if !ok {
		mon = c.Monsters.Monsters[0]
	}
	lvFactor := math.Pow(mon.Growth, float64(req.MonsterLv-1))
	mhp := mon.Base["hp"] * lvFactor
	matk := mon.Base["atk"] * lvFactor
	mdef := mon.Base["def"] * lvFactor
	sr := c.Monsters.SpawnRule
	if v, ok := sr["eliteHpMult"].(float64); ok && req.Elite == "elite" {
		mhp *= v
	}
	if v, ok := sr["eliteAtkMult"].(float64); ok && req.Elite == "elite" {
		matk *= v
	}
	if v, ok := sr["rareEliteHpMult"].(float64); ok && req.Elite == "rareElite" {
		mhp *= v
	}
	if v, ok := sr["rareEliteAtkMult"].(float64); ok && req.Elite == "rareElite" {
		matk *= v
	}

	// 技能队列：按 cd 轮转，全冷却时普攻兜底
	type slotCD struct {
		def    SkillDef
		remain float64
	}
	queue := make([]slotCD, 0, len(req.BD))
	for _, sid := range req.BD {
		if s, ok := c.SkillByID(sid); ok {
			queue = append(queue, slotCD{def: s})
		}
	}
	basic := c.Skills.BasicAttack
	battleCfg := c.Economy.Battle
	tick := num(battleCfg["tickMs"]) / 1000
	if tick <= 0 {
		tick = 1
	}
	maxTicks := int(num(battleCfg["maxSeconds"]) / tick)
	if maxTicks <= 0 {
		maxTicks = 30
	}

	heroHP := hero.HP
	rep := BattleReport{Seed: seed, Rounds: []BattleRound{}}
	heroAtkBuff := 0.0
	monAtkDebuff := 0.0
	shield := 0.0

	// 按速度决定先后手（服务端确定性）
	heroFirst := hero.Spd >= 10
	n := 0
	for t := 0; t < maxTicks && heroHP > 0 && mhp > 0; t++ {
		for _, first := range []bool{heroFirst, !heroFirst} {
			if first {
				// 选一个可用技能（优先级：伤害 > 治疗/护盾，cd 就绪）
				sort.SliceStable(queue, func(i, j int) bool { return queue[i].remain > queue[j].remain })
				chosen := -1
				for i := range queue {
					if queue[i].remain <= 0 {
						chosen = i
						break
					}
				}
				act := basic
				if chosen >= 0 {
					act = queue[chosen].def
					queue[chosen].remain = act.CD
				}
				for i := range queue {
					queue[i].remain = math.Max(0, queue[i].remain-tick)
				}
				n++
				r := BattleRound{N: n, Actor: "hero", Action: act.ID, SkillName: act.Name, FX: act.FX}
				power := act.Power
				switch act.Kind {
				case "heal":
					heal := hero.HP * power
					heroHP = math.Min(hero.HP, heroHP+heal)
					r.Heal = round2(heal)
				case "shield":
					shield += hero.ATK * power
				case "buff":
					heroAtkBuff = power
				case "debuff":
					monAtkDebuff = power
				default: // physical / magic
					dmg := hero.ATK * (1 + heroAtkBuff) * power * (1 - mdef/(mdef+100))
					if act.ID == "execute" && mhp < mon.Base["hp"]*lvFactor*0.3 {
						dmg *= 2
					}
					dmg *= 0.9 + rng.Float64()*0.2 // ±10% 浮动
					actual := dmg
					if shield > 0 {
						absorbed := math.Min(shield, dmg)
						shield -= absorbed
						actual = dmg - absorbed
					}
					mhp -= actual
					r.Damage = round2(actual)
				}
				r.HeroHP = round2(heroHP)
				r.MonHP = round2(math.Max(0, mhp))
				rep.Rounds = append(rep.Rounds, r)
			} else {
				dmg := matk * (1 - monAtkDebuff) * (1 - hero.DEF/(hero.DEF+100))
				dmg *= 0.9 + rng.Float64()*0.2
				actual := dmg
				if shield > 0 {
					absorbed := math.Min(shield, dmg)
					shield -= absorbed
					actual = dmg - absorbed
				}
				heroHP -= actual
				n++
				rep.Rounds = append(rep.Rounds, BattleRound{
					N: n, Actor: "monster", Action: "attack", SkillName: mon.Name,
					FX: "fx_dmg_normal", Damage: round2(actual),
					HeroHP: round2(math.Max(0, heroHP)), MonHP: round2(math.Max(0, mhp)),
				})
			}
			if heroHP <= 0 || mhp <= 0 {
				break
			}
		}
	}
	if mhp <= 0 {
		rep.Result = "win"
	} else {
		rep.Result = "lose"
	}
	rep.Seconds = round2(float64(len(rep.Rounds)) / 2.0 * tick)

	if rep.Result == "win" {
		mult := 1.0
		if req.Elite == "elite" {
			mult = 2
		}
		if req.Elite == "rareElite" {
			mult = 3
		}
		if mon.IsBoss {
			mult = 5
		}
		// 等级系数：Lv1 = 1.0 倍，之后每级 +20%（原公式 Lv1 只有 0.1 倍，前期收益几乎为零）
		lvMult := 1 + 0.2*float64(req.MonsterLv-1)
		rep.Exp = int(mon.Exp * lvMult * mult)
		rep.Gold = int(mon.Gold * lvMult * mult)
		rep.Drops = rollDrops(req, mon, rng)
		rep.Log = "服务端权威结算：胜利，经验/金币/掉落已入账"
	} else {
		rep.Log = "服务端权威结算：战败，无奖励，体力照常消耗，返回大世界原坐标"
	}
	return rep
}

func rollDrops(req BattleRequest, mon MonsterDef, rng *rand.Rand) []EquipItem {
	c := C()
	chance := num(c.Economy.Drop["equipChanceNormal"])
	if req.Elite == "elite" {
		chance = num(c.Economy.Drop["equipChanceElite"])
	}
	if mon.IsBoss {
		chance = num(c.Economy.Drop["equipChanceBoss"])
	}
	out := []EquipItem{}
	for rng.Float64() < chance {
		slots := c.Equipment.Slots
		slot := slots[rng.Intn(len(slots))]
		q := rng.Intn(len(c.Equipment.Quality))
		base := c.Equipment.BaseStatsBySlot[slot]
		stats := map[string]float64{}
		for k, v := range base {
			stats[k] = round2(v * (1 + rng.Float64()*0.3))
		}
		out = append(out, EquipItem{
			ID: mon.ID + "_" + slot + "_" + fmtID(rng.Int63()), Slot: slot, Quality: q, Enhance: 0, Stats: stats,
		})
		if !mon.IsBoss {
			break
		}
	}
	return out
}

func fmtID(n int64) string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 6)
	for i := range b {
		b[i] = chars[n%int64(len(chars))]
		n /= int64(len(chars))
	}
	return string(b)
}
