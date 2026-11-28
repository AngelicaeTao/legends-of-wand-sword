package game

import (
	"math"
	"math/rand"
	"sort"
	"time"
)

// v1.9.77 组队副本 · 三波车轮战
//
// 与旧版（单怪一次结算）的区别：
//   - 一场副本由 3 个波次组成，每波可以是多只怪同屏（3 小怪 / 2 精英 / 1 首领）
//   - 血量跨波不重置，波间不回血（挑战者只能在残血状态下继续打）
//   - 回合数不设上限：打到一方全灭为止（内部只留一个极端的死循环保护）
//   - 任何一波团灭即整场失败，无奖励

// WaveSpec 是一个波次的配置（来自 economy.json 的 dungeons[].waves）
type WaveSpec struct {
	Mon     string  `json:"mon"`
	Lv      int     `json:"lv"`
	Count   int     `json:"count"`
	HpMult  float64 `json:"hpMult"`
	AtkMult float64 `json:"atkMult"`
	Name    string  `json:"name,omitempty"` // 波次名（缺省用怪物名）
}

// PartyHero 是参战成员的战斗快照（由 net 层从存档构造）。
type PartyHero struct {
	UserID  string   `json:"userId"`
	Name    string   `json:"name"`
	ClassID string   `json:"classId"`
	Level   int      `json:"level"`
	Avatar  Avatar   `json:"-"`
	BD      []string `json:"-"`
	StartHP float64  `json:"startHp,omitempty"`
}

// PartyMonster 是波次里的一只怪（战报用）
type PartyMonster struct {
	Name   string  `json:"name"`
	Sprite string  `json:"sprite"`
	Lv     int     `json:"lv"`
	MaxHP  float64 `json:"maxHp"`
	Boss   bool    `json:"isBoss,omitempty"`
}

// PartyRound 是组队战报的一步。
type PartyRound struct {
	N         int       `json:"n"`
	Wave      int       `json:"wave"`  // 1 起
	Actor     string    `json:"actor"` // hero / boss
	HeroIdx   int       `json:"heroIdx,omitempty"`
	MonIdx    int       `json:"monIdx,omitempty"`
	TargetIdx int       `json:"targetIdx,omitempty"` // v1.9.79 治疗/格挡的目标队友
	Action    string    `json:"action,omitempty"`
	SkillName string    `json:"skillName,omitempty"`
	Damage    float64   `json:"damage,omitempty"`
	Heal      float64   `json:"heal,omitempty"`
	MonHP     []float64 `json:"monHp"` // 本波各怪当前血量
	HeroHP    []float64 `json:"heroHp"`
	HeroDead  []bool    `json:"heroDead,omitempty"`
	MonDead   []bool    `json:"monDead,omitempty"`
}

// PartyWaveResult 单波结算
type PartyWaveResult struct {
	Index    int            `json:"index"`
	Rounds   int            `json:"rounds"`
	Result   string         `json:"result"`
	Monsters []PartyMonster `json:"monsters"`
	HeroHP   []float64      `json:"heroHp"`
}

type PartyHeroResult struct {
	UserID  string  `json:"userId"`
	Name    string  `json:"name"`
	ClassID string  `json:"classId"`
	HPStart float64 `json:"hpStart"`
	HPEnd   float64 `json:"hpEnd"`
	Alive   bool    `json:"alive"`
	Damage  float64 `json:"damage"`
}

type PartyBattleReport struct {
	Seed      int64             `json:"seed"`
	DGID      string            `json:"dgId"`
	Result    string            `json:"result"`
	WaveDone  int               `json:"waveDone"` // 通过的波数
	WaveTotal int               `json:"waveTotal"`
	Waves     []PartyWaveResult `json:"waves"`
	Rounds    []PartyRound      `json:"rounds"`
	Heroes    []PartyHeroResult `json:"heroes"`
	Seconds   float64           `json:"seconds"`
	Exp       int               `json:"exp"`
	Gold      int               `json:"gold"`
	PartyMult float64           `json:"partyMult"`
	Log       string            `json:"log"`
}

type partyUnit struct {
	hero    PartyHero
	stats   Stats
	hp      float64
	queue   []partySkill
	hotLeft int
	hotPer  float64
	hotName string
	drLeft  int
	drPct   float64
	shield  float64
	atkBuff float64
	dealt   float64
	// v1.9.82 召唤物协战（术士 wl_summon）
	summonLeft  int
	summonPower float64
	summonName  string
	// v1.9.89 被动特效快照与本单位的英雄回合序号（「每 N 回合」类被动按它计时）
	pb   PassiveBonus
	turn int
}

type partySkill struct {
	def    SkillDef
	remain float64
}

type monUnit struct {
	def  MonsterDef
	hp   float64
	max  float64
	atk  float64
	mdef float64
	lv   int
	dead bool
}

// ResolvePartyBattle 结算整场车轮战（wave 逐波推进，血量不重置）
func ResolvePartyBattle(dgID string, waves []WaveSpec, heroes []PartyHero, seed int64) PartyBattleReport {
	c := C()
	if seed == 0 {
		seed = rand.New(rand.NewSource(time.Now().UnixNano())).Int63()
	}
	rng := rand.New(rand.NewSource(seed))

	units := make([]*partyUnit, 0, len(heroes))
	for _, h := range heroes {
		st := CalcStats(h.Avatar)
		hp := st.HP
		if h.StartHP > 0 && h.StartHP < hp {
			hp = h.StartHP
		}
		// v1.9.89 每个队员的被动特效各自独立结算（与单人 battle.go 同一套 CalcPassiveBonus）
		pb := CalcPassiveBonus(h.Avatar.Passives, h.Avatar.SkillLv)
		u := &partyUnit{hero: h, stats: st, hp: hp, pb: pb, shield: st.ATK * pb.OpenShield}
		for _, sid := range h.BD {
			if s, ok := c.SkillByID(sid); ok {
				// v1.9.89 补上技能等级加成（与单人 battle.go 同口径）：此前组队完全没吃
				// skillUpgrade 的 +8%/级，队员升技能在组队里毫无收益。
				if m := SkillEffectMult(h.Avatar.SkillLv[sid]); m > 1 {
					s.Power *= m
					s.HotPer *= m
					if s.SummonPower > 0 {
						s.SummonPower *= m
					}
					if s.DRPct > 0 {
						s.DRPct *= m
						if s.DRPct > 0.85 {
							s.DRPct = 0.85
						}
					}
				}
				u.queue = append(u.queue, partySkill{def: s})
			}
		}
		units = append(units, u)
	}
	order := make([]int, len(units))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return units[order[a]].stats.Spd > units[order[b]].stats.Spd })

	tick := num(c.Economy.Battle["tickMs"]) / 1000
	if tick <= 0 {
		tick = 1
	}
	// 「无回合上限」：正常打到一方全灭；这里只留一个极端死循环保护（双方都打不动）
	const hardCap = 5000

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
	aliveHeroes := func() int {
		n := 0
		for _, u := range units {
			if u.hp > 0 {
				n++
			}
		}
		return n
	}

	rep := PartyBattleReport{
		Seed: seed, DGID: dgID, WaveTotal: len(waves),
		Rounds: []PartyRound{}, Waves: []PartyWaveResult{},
		Heroes: make([]PartyHeroResult, len(units)),
	}
	for i, u := range units {
		rep.Heroes[i] = PartyHeroResult{UserID: u.hero.UserID, Name: u.hero.Name, ClassID: u.hero.ClassID, HPStart: round2(u.stats.HP)}
	}

	n := 0
	failed := false
	// v1.9.87 减益（诅咒/嘲讽）的降攻效果：整场共享，覆盖式生效（与单人 battle.go 同语义）
	monAtkDebuff := 0.0
	for wi, spec := range waves {
		// —— 构建本波阵容 ——
		base, ok := c.MonsterByID(spec.Mon)
		if !ok {
			base = c.Monsters.Monsters[0]
		}
		lv := spec.Lv
		if lv <= 0 {
			lv = 1
		}
		lvFactor := 1 + (base.Growth-1)*float64(lv-1)
		hpMult := spec.HpMult
		if hpMult <= 0 {
			hpMult = 1
		}
		atkMult := spec.AtkMult
		if atkMult <= 0 {
			atkMult = 1
		}
		cnt := spec.Count
		if cnt <= 0 {
			cnt = 1
		}
		mons := make([]*monUnit, 0, cnt)
		minfos := make([]PartyMonster, 0, cnt)
		for k := 0; k < cnt; k++ {
			m := &monUnit{
				def:  base,
				max:  base.Base["hp"] * lvFactor * hpMult,
				atk:  base.Base["atk"] * lvFactor * atkMult,
				mdef: base.Base["def"] * lvFactor,
				lv:   lv,
			}
			m.hp = m.max
			mons = append(mons, m)
			minfos = append(minfos, PartyMonster{Name: base.Name, Sprite: base.Sprite, Lv: lv, MaxHP: round2(m.max), Boss: cnt == 1 && base.IsBoss})
		}
		aliveMons := func() int {
			k := 0
			for _, m := range mons {
				if m.hp > 0 {
					k++
				}
			}
			return k
		}
		firstAliveMon := func() int {
			for i, m := range mons {
				if m.hp > 0 {
					return i
				}
			}
			return -1
		}
		monHP := func() []float64 {
			out := make([]float64, len(mons))
			for i, m := range mons {
				out[i] = round2(math.Max(0, m.hp))
			}
			return out
		}
		monDead := func() []bool {
			out := make([]bool, len(mons))
			for i, m := range mons {
				out[i] = m.hp <= 0
			}
			return out
		}
		waveStartRounds := len(rep.Rounds)
		heroTarget := 0 // v1.9.77 目标轮转必须跨 tick 累计，否则永远打第一个人

		// —— 本波战斗：直到一方全灭 ——
		for t := 0; t < hardCap && aliveHeroes() > 0 && aliveMons() > 0; t++ {
			for _, idx := range order {
				u := units[idx]
				if u.hp <= 0 || aliveMons() == 0 {
					continue
				}
				u.turn++
				// v1.9.89 被动周期回复（庇护圣所）：每 N 个本人回合回一口，独立战报行
				if u.pb.RegenEvery > 0 && u.pb.RegenPct > 0 && u.turn%u.pb.RegenEvery == 0 && u.hp > 0 && u.hp < u.stats.HP {
					heal := u.stats.HP * u.pb.RegenPct
					if room := u.stats.HP - u.hp; heal > room {
						heal = room
					}
					if heal > 0 {
						u.hp += heal
						n++
						rep.Rounds = append(rep.Rounds, PartyRound{
							N: n, Wave: wi + 1, Actor: "hero", HeroIdx: idx, Action: "passiveRegen",
							SkillName: "被动·回复", Heal: round2(heal),
							MonHP: monHP(), HeroHP: heroHP(), HeroDead: heroDead(), MonDead: monDead(),
						})
					}
				}
				// HoT
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
							N: n, Wave: wi + 1, Actor: "hero", HeroIdx: idx, Action: "hot",
							SkillName: u.hotName + "（持续）", Heal: round2(jump),
							MonHP: monHP(), HeroHP: heroHP(), HeroDead: heroDead(), MonDead: monDead(),
						})
					}
				}
				// v1.9.82 召唤物协战：本人行动前先结算一次（独立战报行）
				if u.summonLeft > 0 && aliveMons() > 0 {
					u.summonLeft--
					if mi := firstAliveMon(); mi >= 0 {
						tgt := mons[mi]
						dmg := u.stats.ATK * u.summonPower * (1 - tgt.mdef/(tgt.mdef+100))
						dmg *= 0.9 + rng.Float64()*0.2
						if dmg > tgt.hp {
							dmg = tgt.hp
						}
						if dmg < 0 {
							dmg = 0
						}
						tgt.hp -= dmg
						n++
						rep.Rounds = append(rep.Rounds, PartyRound{
							N: n, Wave: wi + 1, Actor: "hero", HeroIdx: idx, Action: "summonHit",
							SkillName: u.summonName + "（协战）", Damage: round2(dmg), MonIdx: mi,
							MonHP: monHP(), HeroHP: heroHP(), HeroDead: heroDead(), MonDead: monDead(),
						})
					}
					if aliveMons() == 0 {
						break
					}
				}
				// 技能 CD
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
				// v1.9.79 治疗/格挡优先照顾血量比例最低的队友（含自己）
				pickAlly := func() int {
					best, bestR := -1, 2.0
					for i, v := range units {
						if v.hp <= 0 {
							continue
						}
						r := v.hp / math.Max(1, v.stats.HP)
						if r < bestR {
							bestR, best = r, i
						}
					}
					return best
				}
				var act SkillDef
				if chosen >= 0 {
					act = u.queue[chosen].def
					// v1.9.89 被动冷却缩减：下限 1 秒
					cd := act.CD * (1 - u.pb.CDR)
					if cd < 1 {
						cd = 1
					}
					u.queue[chosen].remain = cd
				} else {
					act = c.Skills.BasicAttack
				}
				// 治疗：没人是残血就改成普攻，不浪费回合
				tgtAlly := -1
				if act.Kind == "heal" || act.Kind == "shield" {
					tgtAlly = pickAlly()
				}
				if act.Kind == "heal" {
					// v1.9.79 队友血量低于 75% 才治疗，否则转普攻保输出
					need := tgtAlly >= 0 && units[tgtAlly].hp < units[tgtAlly].stats.HP*0.75
					if !need {
						act = c.Skills.BasicAttack
					}
				}
				rd := PartyRound{N: n + 1, Wave: wi + 1, Actor: "hero", HeroIdx: idx, Action: act.ID, SkillName: act.Name}
				// 只有真正的治疗/格挡才标注目标（转普攻的那次不标）
				if (act.Kind == "heal" || act.Kind == "shield") && tgtAlly >= 0 && tgtAlly != idx {
					rd.TargetIdx = tgtAlly
				}
				n++
				// v1.9.87 伤害结算闭包：组队里 physical/magic 与「带伤害的减益」共用，
				// 含 ±10% 浮动、目标血量封顶、吸血回血，口径与单人 battle.go 对齐
				dealDamage := func(mult float64) {
					mi := firstAliveMon()
					if mi < 0 {
						return
					}
					tgt := mons[mi]
					dmg := u.stats.ATK * (1 + u.atkBuff) * mult * (1 - tgt.mdef/(tgt.mdef+100))
					// v1.9.89 被动增伤：按技能类型 + 低血狂暴（与单人同口径）
					if v := u.pb.KindDmgOf(act.Kind); v > 0 {
						dmg *= 1 + v
					}
					if u.pb.LowHPDmg > 0 {
						dmg *= u.pb.LowHPMult(u.hp, u.stats.HP)
					}
					dmg *= 0.9 + rng.Float64()*0.2
					if dmg > tgt.hp {
						dmg = tgt.hp
					}
					tgt.hp -= dmg
					u.dealt += dmg
					rd.MonIdx = mi
					rd.Damage = round2(dmg)
					// v1.9.82 吸取：命中回复本次伤害的 lifesteal（按目标剩余血量计，防溢出）
					// v1.9.89 技能自带吸血 + 被动附加吸血合并结算
					ls := act.Lifesteal + u.pb.Lifesteal
					if ls > 0 && dmg > 0 {
						heal := dmg * ls
						if room := u.stats.HP - u.hp; heal > room {
							heal = room
						}
						if heal > 0 {
							u.hp += heal
							rd.Heal = round2(heal)
						}
					}
				}
				switch act.Kind {
				case "heal":
					tu := u
					if tgtAlly >= 0 {
						tu = units[tgtAlly]
					}
					if act.HotRounds > 0 {
						tu.hotLeft = act.HotRounds
						// v1.9.89 治疗增幅按施放者的被动算（恩泽属于祭祀自己）
						tu.hotPer = tu.stats.HP * act.HotPer * (1 + u.pb.HealBoost)
						tu.hotName = act.Name
						jump := tu.hotPer
						if healed := tu.stats.HP - tu.hp; jump > healed {
							jump = healed
						}
						if jump > 0 {
							tu.hp += jump
							rd.Heal = round2(jump)
						}
					} else {
						heal := tu.stats.HP * act.Power * (1 + u.pb.HealBoost)
						if room := tu.stats.HP - tu.hp; heal > room {
							heal = room
						}
						if heal > 0 {
							tu.hp += heal
							rd.Heal = round2(heal)
						}
					}
				case "shield":
					tu := u
					if tgtAlly >= 0 {
						tu = units[tgtAlly]
					}
					if act.DRRounds > 0 {
						tu.drLeft = act.DRRounds
						tu.drPct = act.DRPct
					} else {
						// v1.9.89 护盾增幅按施放者的被动算（圣盾精通属于骑士自己）
						tu.shield += u.stats.ATK * act.Power * (1 + u.pb.ShieldBoost)
					}
					rd.TargetIdx = tgtAlly
				case "buff":
					u.atkBuff = act.Power
				case "debuff":
					// v1.9.87 减益：power=伤害系数（0 则纯降攻不出手），atkDown=怪物攻击降低比例
					// v1.9.89 被动降攻强化，上限 90%
					monAtkDebuff = math.Min(0.9, act.AtkDown*(1+u.pb.DebuffBoost))
					if act.Power > 0 {
						dealDamage(act.Power)
					}
				case "summon":
					// v1.9.82 召唤：当回合登场不出手，之后每个本人回合由召唤物补伤害
					if act.SummonRounds > 0 {
						u.summonLeft = act.SummonRounds
						u.summonPower = act.SummonPower
						u.summonName = act.Name
					}
				default:
					dealDamage(act.Power)
				}
				rd.MonHP = monHP()
				rd.HeroHP = heroHP()
				rd.HeroDead = heroDead()
				rd.MonDead = monDead()
				rep.Rounds = append(rep.Rounds, rd)
				// v1.9.89 被动追击（疾风步）：每 N 个本人回合在技能之后追加一击
				if u.pb.ExtraEvery > 0 && u.pb.ExtraPower > 0 && u.turn%u.pb.ExtraEvery == 0 && u.hp > 0 && aliveMons() > 0 {
					if mi := firstAliveMon(); mi >= 0 {
						tgt := mons[mi]
						xd := u.stats.ATK * u.pb.ExtraPower * (1 - tgt.mdef/(tgt.mdef+100))
						xd *= u.pb.LowHPMult(u.hp, u.stats.HP)
						xd *= 0.9 + rng.Float64()*0.2
						if xd > tgt.hp {
							xd = tgt.hp
						}
						if xd < 0 {
							xd = 0
						}
						tgt.hp -= xd
						u.dealt += xd
						n++
						rep.Rounds = append(rep.Rounds, PartyRound{
							N: n, Wave: wi + 1, Actor: "hero", HeroIdx: idx, Action: "passiveExtra",
							SkillName: "被动·追击", Damage: round2(xd), MonIdx: mi,
							MonHP: monHP(), HeroHP: heroHP(), HeroDead: heroDead(), MonDead: monDead(),
						})
					}
				}
			}
			if aliveHeroes() == 0 || aliveMons() == 0 {
				break
			}
			// —— 怪物出手：本波每只存活怪各行动一次（车轮战的压迫感来源）——
			for _, m := range mons {
				if m.hp <= 0 {
					continue
				}
				targets := make([]int, 0, len(units))
				for i, u := range units {
					if u.hp > 0 {
						targets = append(targets, i)
					}
				}
				if len(targets) == 0 {
					break
				}
				ti := targets[heroTarget%len(targets)]
				heroTarget++
				tu := units[ti]
				dmg := m.atk * (1 - monAtkDebuff) * (1 - tu.stats.DEF/(tu.stats.DEF+100))
				if tu.drLeft > 0 {
					dmg *= 1 - tu.drPct
					tu.drLeft--
				}
				if tu.pb.DR > 0 {
					dmg *= 1 - tu.pb.DR
				} // v1.9.89 被动常驻受伤减免
				dmg *= 0.9 + rng.Float64()*0.2
				if tu.shield > 0 {
					absorbed := math.Min(tu.shield, dmg)
					tu.shield -= absorbed
					dmg -= absorbed
				}
				tu.hp -= dmg
				n++
				rep.Rounds = append(rep.Rounds, PartyRound{
					N: n, Wave: wi + 1, Actor: "boss", HeroIdx: ti, SkillName: m.def.Name, Damage: round2(dmg),
					MonHP: monHP(), HeroHP: heroHP(), HeroDead: heroDead(), MonDead: monDead(),
				})
				// v1.9.89 被动反伤（荆棘之甲）：按实际受到的伤害反弹给攻击者，同回合战报行
				if tu.pb.Thorns > 0 && dmg > 0 && m.hp > 0 {
					rf := dmg * tu.pb.Thorns
					if rf > m.hp {
						rf = m.hp
					}
					m.hp -= rf
					rep.Rounds = append(rep.Rounds, PartyRound{
						N: n, Wave: wi + 1, Actor: "hero", HeroIdx: ti, Action: "passiveThorns",
						SkillName: "被动·反伤", Damage: round2(rf),
						MonHP: monHP(), HeroHP: heroHP(), HeroDead: heroDead(), MonDead: monDead(),
					})
				}
				if aliveHeroes() == 0 {
					break
				}
			}
		}

		waveWin := aliveMons() == 0 && aliveHeroes() > 0
		res := "lose"
		if waveWin {
			res = "win"
		}
		rep.Waves = append(rep.Waves, PartyWaveResult{
			Index: wi + 1, Rounds: len(rep.Rounds) - waveStartRounds, Result: res,
			Monsters: minfos, HeroHP: heroHP(),
		})
		if !waveWin {
			failed = true
			break
		}
		rep.WaveDone = wi + 1
	}

	if !failed && rep.WaveDone == len(waves) && aliveHeroes() > 0 {
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

	// 奖励：全通才发；人数加成封顶 1.5 倍
	mult := 1 + 0.25*float64(len(units)-1)
	if mult > 1.5 {
		mult = 1.5
	}
	rep.PartyMult = mult
	if rep.Result == "win" {
		expSum, goldSum, lvSum := 0.0, 0.0, 0
		for _, w := range waves {
			if b, ok := c.MonsterByID(w.Mon); ok {
				expSum += b.Exp * float64(maxInt(w.Count, 1))
				goldSum += b.Gold * float64(maxInt(w.Count, 1))
			}
			lvSum += maxInt(w.Lv, 1)
		}
		lvMult := 1 + 0.2*float64(lvSum/maxInt(len(waves), 1)-1)
		rep.Exp = int(expSum * lvMult * mult)
		rep.Gold = int(goldSum * lvMult * mult)
		rep.Log = "组队副本：三波全通"
	} else {
		rep.Exp, rep.Gold = 0, 0
		rep.Log = "组队副本：车轮战失败，无奖励"
	}
	return rep
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
