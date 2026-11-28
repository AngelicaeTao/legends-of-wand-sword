package game

import (
	"math"
	"math/rand"
	"sort"
	"time"
)

// v1.9.68 组队副本：多人共享同一个 BOSS 的真联机战斗。
//
// 与单人战斗的区别：
//   - 每个成员有独立的当前血量，血量归零后不再出手，但战斗继续（其余人还能打）
//   - BOSS 血量按人数放大（bossHpMult），奖励按人数加成（封顶 1.5 倍）
//   - 战报按回合记录"每一步后的全体血量快照"，前端据此在头顶画血条
//
// 设计取舍：为了让三份血量能同时演出，这里把整场战斗一次性算完再回放，
// 而不是实时同步。自动战斗没有玩家操作，一次性结算 + 回放与实时同步体验一致，
// 但省掉了帧同步、掉线补偿、乱序处理这一整类问题。

// PartyHero 是参战成员的战斗快照（由 net 层从存档构造）。
type PartyHero struct {
	UserID  string   `json:"userId"`
	Name    string   `json:"name"`
	ClassID string   `json:"classId"`
	Level   int      `json:"level"`
	Avatar  Avatar   `json:"-"`  // 属性来源
	BD      []string `json:"-"`  // 技能队列
}

// PartyRound 是组队战报的一步。
type PartyRound struct {
	N        int       `json:"n"`
	Actor    string    `json:"actor"` // hero / boss
	HeroIdx  int       `json:"heroIdx,omitempty"`
	Action   string    `json:"action,omitempty"`
	SkillName string   `json:"skillName,omitempty"`
	Damage   float64   `json:"damage,omitempty"`
	Heal     float64   `json:"heal,omitempty"`
	BossHP   float64   `json:"bossHp"`
	HeroHP   []float64 `json:"heroHp"` // 各成员当前血量（与成员顺序一致）
	HeroDead []bool    `json:"heroDead,omitempty"`
}

// PartyHeroResult 是单个成员的结算结果。
type PartyHeroResult struct {
	UserID string  `json:"userId"`
	Name   string  `json:"name"`
	ClassID string `json:"classId"`
	HPStart float64 `json:"hpStart"`
	HPEnd   float64 `json:"hpEnd"`
	Alive   bool    `json:"alive"`
	Damage  float64 `json:"damage"` // 本次副本造成总伤害
}

// PartyBattleReport 组队副本战报。
type PartyBattleReport struct {
	Seed      int64             `json:"seed"`
	DGID      string            `json:"dgId"`
	Result    string            `json:"result"` // win/lose
	BossName  string            `json:"bossName"`
	BossMaxHP float64           `json:"bossMaxHp"`
	Rounds    []PartyRound      `json:"rounds"`
	Heroes    []PartyHeroResult `json:"heroes"`
	Seconds   float64           `json:"seconds"`
	Exp       int               `json:"exp"`
	Gold      int               `json:"gold"`
	PartyMult float64           `json:"partyMult"`
	Log       string            `json:"log"`
}

type partyUnit struct {
	hero      PartyHero
	stats     Stats
	hp        float64
	queue     []partySkill
	hotLeft   int
	hotPer    float64
	hotName   string
	drLeft    int
	drPct     float64
	shield    float64
	atkBuff   float64
	dealt     float64
}

type partySkill struct {
	def    SkillDef
	remain float64
}

// ResolvePartyBattle 结算一场组队副本。
// heroes 至少 1 人（单人也能用同一套逻辑）。
func ResolvePartyBattle(dgID, bossID string, bossLv int, bossHpMult float64, heroes []PartyHero, seed int64) PartyBattleReport {
	c := C()
	if seed == 0 {
		seed = time.Now().UnixNano()
	}
	rng := rand.New(rand.NewSource(seed))

	boss, ok := c.MonsterByID(bossID)
	if !ok {
		boss = c.Monsters.Monsters[0]
	}
	sr := c.Monsters.SpawnRule
	_ = sr
	lvFactor := 1 + (boss.Growth-1)*float64(maxInt(bossLv, 1)-1)
	// v1.9.68 人数系数：每多一名队员 BOSS 血量 +75%（封顶 2.5 倍），
	// 保证单人挑战明显更吃力、三人齐又要一起输出约 20~25 回合。
	partyScale := 1 + 0.75*float64(len(heroes)-1)
	if partyScale > 2.5 {
		partyScale = 2.5
	}
	if bossHpMult <= 0 {
		bossHpMult = 1
	}
	bossMax := boss.Base["hp"] * lvFactor * bossHpMult * partyScale
	bossATK := boss.Base["atk"] * lvFactor
	bossDEF := boss.Base["def"] * lvFactor
	bossHP := bossMax
	if bossHP < 1 {
		bossHP = 1
	}

	units := make([]*partyUnit, 0, len(heroes))
	for _, h := range heroes {
		st := CalcStats(h.Avatar)
		u := &partyUnit{hero: h, stats: st, hp: st.HP}
		for _, sid := range h.BD {
			if s, ok := c.SkillByID(sid); ok {
				u.queue = append(u.queue, partySkill{def: s})
			}
		}
		units = append(units, u)
	}
	// 速度决定出手顺序（目前六职业都是 10，稳定排序即可）
	order := make([]int, len(units))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return units[order[a]].stats.Spd > units[order[b]].stats.Spd })

	battleCfg := c.Economy.Battle
	tick := num(battleCfg["tickMs"]) / 1000
	if tick <= 0 {
		tick = 1
	}
	maxTicks := int(num(battleCfg["maxSeconds"]) / tick)
	if maxTicks <= 0 {
		maxTicks = 30
	}

	heroHP := func() []float64 {
		out := make([]float64, len(units))
		for i, u := range units {
			out[i] = round2(math.Max(0, u.hp))
		}
		return out
	}
	heroDead := func() []bool {
		out := make([]bool, len(units))
		for i, u := range units {
			out[i] = u.hp <= 0
		}
		return out
	}
	aliveCount := func() int {
		n := 0
		for _, u := range units {
			if u.hp > 0 {
				n++
			}
		}
		return n
	}

	rep := PartyBattleReport{
		Seed: seed, DGID: dgID, BossName: boss.Name, BossMaxHP: round2(bossMax),
		Rounds: []PartyRound{}, Heroes: make([]PartyHeroResult, len(units)),
	}
	for i, u := range units {
		rep.Heroes[i] = PartyHeroResult{UserID: u.hero.UserID, Name: u.hero.Name, ClassID: u.hero.ClassID, HPStart: round2(u.stats.HP)}
	}

	n := 0
	bossTurn := 0
	for t := 0; t < maxTicks && bossHP > 0 && aliveCount() > 0; t++ {
		for _, idx := range order {
			u := units[idx]
			if u.hp <= 0 {
				continue
			}
			// HoT 跳数
			if u.hotLeft > 0 {
				u.hotLeft--
				jump := u.hotPer
				if healed := u.stats.HP - u.hp; jump > healed {
					jump = healed
				}
				if jump > 0 {
					u.hp += jump
					n++
					rep.Rounds = append(rep.Rounds, PartyRound{
						N: n, Actor: "hero", HeroIdx: idx, Action: "hot",
						SkillName: u.hotName + "（持续）", Heal: round2(jump),
						BossHP: round2(math.Max(0, bossHP)), HeroHP: heroHP(), HeroDead: heroDead(),
					})
				}
			}
			if bossHP <= 0 {
				break
			}
			// 选技能：就绪里优先伤害
			for i := range u.queue {
				if u.queue[i].remain > 0 {
					u.queue[i].remain -= tick
				}
			}
			sort.SliceStable(u.queue, func(a, b int) bool { return u.queue[a].remain > u.queue[b].remain })
			chosen := -1
			for i := range u.queue {
				if u.queue[i].remain <= 0 {
					chosen = i
					break
				}
			}
			var act SkillDef
			ready := chosen >= 0
			if ready {
				act = u.queue[chosen].def
				u.queue[chosen].remain = act.CD
			}
			if !ready {
				act = c.Skills.BasicAttack
			}
			n++
			rd := PartyRound{N: n, Actor: "hero", HeroIdx: idx, Action: act.ID, SkillName: act.Name}
			switch act.Kind {
			case "heal":
				if act.HotRounds > 0 {
					u.hotLeft = act.HotRounds
					u.hotPer = u.stats.HP * act.HotPer
					u.hotName = act.Name
					if jump := u.hotPer; jump > 0 {
						if healed := u.stats.HP - u.hp; jump > healed {
							jump = healed
						}
						if jump > 0 {
							u.hp += jump
							rd.Heal = round2(jump)
						}
					}
				} else {
					heal := u.stats.HP * act.Power
					if room := u.stats.HP - u.hp; heal > room {
						heal = room
					}
					if heal > 0 {
						u.hp += heal
						rd.Heal = round2(heal)
					}
				}
			case "shield":
				if act.DRRounds > 0 {
					u.drLeft = act.DRRounds
					u.drPct = act.DRPct
				} else {
					u.shield += u.stats.ATK * act.Power
				}
			case "buff":
				u.atkBuff = act.Power
			default:
				dmg := u.stats.ATK * (1 + u.atkBuff) * act.Power * (1 - bossDEF/(bossDEF+100))
				dmg *= 0.9 + rng.Float64()*0.2
				if u.shield > 0 { // 己方护盾不参与对怪输出，这里仅保持数值一致
					_ = u.shield
				}
				if dmg > bossHP {
					dmg = bossHP
				}
				bossHP -= dmg
				u.dealt += dmg
				rd.Damage = round2(dmg)
			}
			rd.BossHP = round2(math.Max(0, bossHP))
			rd.HeroHP = heroHP()
			rd.HeroDead = heroDead()
			rep.Rounds = append(rep.Rounds, rd)
			if bossHP <= 0 {
				break
			}
		}
		if bossHP <= 0 || aliveCount() == 0 {
			break
		}
		// BOSS 出手：轮流打存活成员
		targets := make([]int, 0, len(units))
		for i, u := range units {
			if u.hp > 0 {
				targets = append(targets, i)
			}
		}
		if len(targets) == 0 {
			break
		}
		ti := targets[bossTurn%len(targets)]
		bossTurn++
		tu := units[ti]
		dmg := bossATK * (1 - tu.stats.DEF/(tu.stats.DEF+100))
		if tu.drLeft > 0 {
			dmg *= 1 - tu.drPct
			tu.drLeft--
		}
		dmg *= 0.9 + rng.Float64()*0.2
		if tu.shield > 0 {
			absorbed := math.Min(tu.shield, dmg)
			tu.shield -= absorbed
			dmg -= absorbed
		}
		tu.hp -= dmg
		n++
		rep.Rounds = append(rep.Rounds, PartyRound{
			N: n, Actor: "boss", HeroIdx: ti, SkillName: boss.Name, Damage: round2(dmg),
			BossHP: round2(math.Max(0, bossHP)), HeroHP: heroHP(), HeroDead: heroDead(),
		})
	}

	if bossHP <= 0 {
		rep.Result = "win"
	} else {
		rep.Result = "lose"
	}
	rep.Seconds = round2(float64(len(rep.Rounds)) / 2.0 * tick)
	for i, u := range units {
		rep.Heroes[i].HPEnd = round2(math.Max(1, u.hp))
		rep.Heroes[i].Alive = u.hp > 0
		rep.Heroes[i].Damage = round2(u.dealt)
	}

	// 奖励：基础值 × 人数加成（封顶 1.5 倍）
	mult := 1 + 0.25*float64(len(units)-1)
	if mult > 1.5 {
		mult = 1.5
	}
	rep.PartyMult = mult
	if rep.Result == "win" {
		lvMult := 1 + 0.2*float64(maxInt(bossLv, 1)-1)
		rep.Exp = int(boss.Exp * lvMult * mult)
		rep.Gold = int(boss.Gold * lvMult * mult)
		rep.Log = "组队副本：胜利，奖励按人数加成结算"
	} else {
		rep.Log = "组队副本：败北，无奖励（血量按剩余值保留）"
	}
	return rep
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
