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
	Elite     string   `json:"elite"`           // ""/elite/rareElite
	Seed      int64    `json:"seed"`            // >0 时用于复现（playtest 回放）；=0 由服务端随机生成
	StartHP   float64  `json:"startHp"`         // v1.9.27 全局血量：战斗以该血量开局（>0 且 < 最大血量时生效）
	Group     []string `json:"group,omitempty"` // v1.9.36 群怪模版：怪物组合（服务端权威点位下发，可混编）；空=单怪
	// v1.9.53 推进进度 0~1（W1=0/W6=1），由服务端按存档填写，用于首领动态强度；客户端传值无效
	Progress float64 `json:"progress,omitempty"`
	// v1.9.56 技能等级（技能id→等级），由服务端按存档填写；每级效果 +8%，客户端传值无效
	SkillLv map[string]int `json:"-"`
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
	DR        float64 `json:"dr,omitempty"` // v1.9.40 本回合怪物伤害被持续减伤降低的比例（0=无）
	HeroHP    float64 `json:"heroHp"`
	MonHP     float64 `json:"monHp"`
	MonIdx    int     `json:"monIdx,omitempty"` // v1.9.36 群怪：本回合怪物出手者/英雄攻击目标的组合索引
}

// MonInfo 战报下发的怪物方成员（v1.9.36 群怪贴图/名牌以此渲染，与结算一致）。
type MonInfo struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Sprite string `json:"sprite"`
	Lv     int    `json:"lv"`
	Elite  bool   `json:"elite"`
	IsBoss bool   `json:"isBoss"`
}

// DecompItem 是"自动分解"的逐件明细（v1.9.59）：结算界面据此列出每件装备换到了什么。
type DecompItem struct {
	Slot    string `json:"slot"`
	Quality int    `json:"quality"`
	ClassID string `json:"classId,omitempty"`
	Gold    int    `json:"gold"`
	Mat     int    `json:"mat"`
}

// BattleReport 服务端下发的权威战报。客户端只做表现，不参与结算。
type BattleReport struct {
	Seed   int64  `json:"seed"`
	Result string `json:"result"` // win/lose
	// v1.9.58 秒杀：遭遇总血量低于普攻保底伤害时直接判胜，Rounds 为空、前端跳过战斗演出
	OneShot bool          `json:"oneShot,omitempty"`
	Rounds  []BattleRound `json:"rounds"`
	Exp     int           `json:"exp"`
	Gold    int           `json:"gold"`
	Drops   []EquipItem   `json:"drops"`
	// v1.9.54 低于同部位在穿战力的掉落已在入包前分解，不再出现在 drops 里；
	// 这里只带汇总，前端据此显示"自动分解 N 件 · 金币 +X · 材料 +Y"。
	DecomposedCount int `json:"decomposedCount,omitempty"`
	DecomposedGold  int `json:"decomposedGold,omitempty"`
	DecomposedMat   int `json:"decomposedMat,omitempty"`
	// v1.9.59 分解逐件明细：结算界面要能写清"每件分解出了什么"，而不只是总数
	Decomposed  []DecompItem `json:"decomposed,omitempty"`
	Seconds     float64      `json:"seconds"`
	Log         string       `json:"log"`
	HeroHPStart float64      `json:"heroHpStart"` // v1.9.27 战斗开局血量（全局血量不回满）
	HeroHPEnd   float64      `json:"heroHpEnd"`   // 战斗结束血量（战败保底 1，防软锁）
	Monsters    []MonInfo    `json:"monsters"`    // v1.9.36 群怪：敌方阵容（单怪时长度 1，贴图与结算一致）
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

	// v1.9.36 群怪模版：组合非空按组合展开成员（服务端权威点位下发，可混编不同怪），否则单怪。
	group := req.Group
	if len(group) == 0 {
		group = []string{req.MonsterID}
	}
	if len(group) > 3 {
		group = group[:3]
	} // 上限 3 只，防异常请求
	type monUnit struct {
		def      MonsterDef
		hp, atk  float64
		mdef     float64
		lvFactor float64
	}
	mems := make([]monUnit, 0, len(group))
	monInfos := []MonInfo{}
	sr := c.Monsters.SpawnRule
	for gi, gid := range group {
		m, ok := c.MonsterByID(gid)
		if !ok {
			m = c.Monsters.Monsters[0]
		}
		// v1.9.43 高等级削峰：成长由指数 growth^(lv-1) 改为线性 1+(growth-1)×(lv-1)。
		// 玩家每级是线性成长（+18血/+3攻），指数在 Lv20+ 会失控（旧公式 Boss Lv20 攻 604，一刀秒人）。
		lvFactor := 1 + (m.Growth-1)*float64(req.MonsterLv-1)
		u := monUnit{def: m, hp: m.Base["hp"] * lvFactor, atk: m.Base["atk"] * lvFactor, mdef: m.Base["def"] * lvFactor, lvFactor: lvFactor}
		// 精英/稀有精英倍率只作用于主怪（组合首位，与点位 elite 语义一致）。
		// v1.9.43：Boss 自身已是首领强度，不再叠加精英倍率（原双重强化导致高级副本必败）。
		isMain := gi == 0
		noEliteStack := isMain && m.IsBoss
		if isMain && !noEliteStack {
			if v, ok := sr["eliteHpMult"].(float64); ok && req.Elite == "elite" {
				u.hp *= v
			}
			if v, ok := sr["eliteAtkMult"].(float64); ok && req.Elite == "elite" {
				u.atk *= v
			}
			if v, ok := sr["rareEliteHpMult"].(float64); ok && req.Elite == "rareElite" {
				u.hp *= v
			}
			if v, ok := sr["rareEliteAtkMult"].(float64); ok && req.Elite == "rareElite" {
				u.atk *= v
			}
		}
		// v1.9.53 图域首领动态强度：血量按挑战者相对同级裸装的战力倍率放大，攻击按目标承伤比例反解
		if isMain && m.ScaleWithPlayer {
			if hpMul, atkAbs, ok := bossScale(req); ok {
				u.hp *= hpMul
				if atkAbs > 0 {
					u.atk = atkAbs
				}
			}
		}
		mems = append(mems, u)
		monInfos = append(monInfos, MonInfo{
			ID: m.ID, Name: m.Name, Sprite: m.Sprite, Lv: req.MonsterLv,
			// v1.9.46：boss 档不再被标成"精英"（首个成员且请求显式声明精英时才算精英）
			Elite: gi == 0 && (req.Elite == "elite" || req.Elite == "rareElite"), IsBoss: m.IsBoss,
		})
	}
	mon := mems[0].def // 主怪（兼容奖励/掉落逻辑）

	// 技能队列：按 cd 轮转，全冷却时普攻兜底
	type slotCD struct {
		def    SkillDef
		remain float64
	}
	queue := make([]slotCD, 0, len(req.BD))
	for _, sid := range req.BD {
		if s, ok := c.SkillByID(sid); ok {
			// v1.9.56 技能等级加成：power/heal/减伤 同乘 (1+0.08×(lv-1))
			if m := SkillEffectMult(req.SkillLv[sid]); m > 1 {
				s.Power *= m
				s.HotPer *= m
				if s.DRPct > 0 {
					s.DRPct *= m
					if s.DRPct > 0.85 { // 减伤封顶 85%，避免高等级直接免疫
						s.DRPct = 0.85
					}
				}
			}
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

	// v1.9.27 全局血量：以存档当前血量开局（不每次回满）；异常值兜底满血
	heroHP := hero.HP
	if req.StartHP > 0 && req.StartHP < hero.HP {
		heroHP = req.StartHP
	}
	rep := BattleReport{Seed: seed, Rounds: []BattleRound{}, HeroHPStart: round2(heroHP), Monsters: monInfos}
	heroAtkBuff := 0.0
	monAtkDebuff := 0.0
	shield := 0.0
	// v1.9.40 持续效果状态：HoT（法师治疗术持续回血）与 DR（战士格挡持续减伤）
	hotLeft := 0
	hotPer := 0.0
	hotName := ""
	drLeft := 0
	drPct := 0.0

	// 按速度决定先后手（服务端确定性）
	heroFirst := hero.Spd >= 10
	totalMonHP := func() float64 {
		s := 0.0
		for i := range mems {
			s += mems[i].hp
		}
		return s
	}
	// v1.9.36 群怪：怪物轮转出手（死亡的自动跳过），英雄攻击第一个存活目标
	turnIdx := 0
	nextAlive := func(from int) int {
		for k := 0; k < len(mems); k++ {
			i := (from + k) % len(mems)
			if mems[i].hp > 0 {
				return i
			}
		}
		return -1
	}
	n := 0
	// v1.9.58 秒杀判定：本次遭遇总血量 ≤ 普攻保底伤害时，直接判胜、跳过整个回合模拟。
	// 保底伤害取最保守口径：普攻 power × 最低浮动 0.9 × 组合内最高防御减伤，判定必杀即真必杀。
	// 玩家先手（heroFirst）才允许，避免"还没出手就被打死"却被判胜。
	oneShot := false
	if heroFirst {
		maxMdef := 0.0
		for i := range mems {
			if mems[i].mdef > maxMdef {
				maxMdef = mems[i].mdef
			}
		}
		basicDmg := hero.ATK * basic.Power * (1 - maxMdef/(maxMdef+100)) * 0.9
		if basicDmg > 0 && totalMonHP() <= basicDmg {
			oneShot = true
			maxTicks = 0
		}
	}
	for t := 0; t < maxTicks && heroHP > 0 && totalMonHP() > 0; t++ {
		for _, first := range []bool{heroFirst, !heroFirst} {
			if first {
				// v1.9.40 HoT 跳数结算：英雄行动前先回一口（独立战报行，前端绿色浮字）
				if hotLeft > 0 && heroHP > 0 {
					hotLeft--
					jump := hotPer
					healed := hero.HP - heroHP
					if jump > healed {
						jump = healed
					}
					if jump > 0 {
						heroHP += jump
					}
					n++
					rep.Rounds = append(rep.Rounds, BattleRound{
						N: n, Actor: "hero", Action: "hot", SkillName: hotName + "（持续）",
						FX: "fx_heal", Heal: round2(jump),
						HeroHP: round2(heroHP), MonHP: round2(math.Max(0, totalMonHP())),
					})
				}
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
				ti := nextAlive(turnIdx) // 英雄攻击目标：第一个存活怪
				if ti < 0 {
					ti = 0
				}
				tgt := &mems[ti]
				r.MonIdx = ti
				switch act.Kind {
				case "heal":
					if act.HotRounds > 0 {
						// v1.9.40 持续回血：施放立即回第一跳，剩余 HotRounds-1 跳逐回合结算
						hotPer = hero.HP * act.HotPer
						hotLeft = act.HotRounds - 1
						hotName = act.Name
						heal := hotPer
						healed := hero.HP - heroHP
						if heal > healed {
							heal = healed
						}
						if heal > 0 {
							heroHP += heal
						}
						r.Heal = round2(heal)
					} else {
						heal := hero.HP * power
						heroHP = math.Min(hero.HP, heroHP+heal)
						r.Heal = round2(heal)
					}
				case "shield":
					if act.DRRounds > 0 {
						// v1.9.40 持续减伤：不再叠加护盾值，改为 DRRounds 回合内按比例减伤
						drLeft = act.DRRounds
						drPct = act.DRPct
					} else {
						shield += hero.ATK * power
					}
				case "buff":
					heroAtkBuff = power
				case "debuff":
					monAtkDebuff = power
				default: // physical / magic
					dmg := hero.ATK * (1 + heroAtkBuff) * power * (1 - tgt.mdef/(tgt.mdef+100))
					if act.ID == "execute" && tgt.hp < tgt.def.Base["hp"]*tgt.lvFactor*0.3 {
						dmg *= 2
					}
					dmg *= 0.9 + rng.Float64()*0.2 // ±10% 浮动
					actual := dmg
					if shield > 0 {
						absorbed := math.Min(shield, dmg)
						shield -= absorbed
						actual = dmg - absorbed
					}
					tgt.hp -= actual
					r.Damage = round2(actual)
				}
				r.HeroHP = round2(heroHP)
				r.MonHP = round2(math.Max(0, totalMonHP()))
				rep.Rounds = append(rep.Rounds, r)
			} else {
				mi := nextAlive(turnIdx)
				if mi < 0 {
					break
				}
				atkUnit := &mems[mi]
				turnIdx = (mi + 1) % len(mems) // 下回合轮到下一只
				dmg := atkUnit.atk * (1 - monAtkDebuff) * (1 - hero.DEF/(hero.DEF+100))
				if drLeft > 0 {
					dmg *= 1 - drPct
				} // v1.9.40 格挡姿态持续减伤
				dmg *= 0.9 + rng.Float64()*0.2
				actual := dmg
				if shield > 0 {
					absorbed := math.Min(shield, dmg)
					shield -= absorbed
					actual = dmg - absorbed
				}
				heroHP -= actual
				n++
				mr := BattleRound{
					N: n, Actor: "monster", Action: "attack", SkillName: atkUnit.def.Name,
					FX: "fx_dmg_normal", Damage: round2(actual), MonIdx: mi,
					HeroHP: round2(math.Max(0, heroHP)), MonHP: round2(math.Max(0, totalMonHP())),
				}
				if drLeft > 0 {
					mr.DR = drPct
				} // 前端据此显示"格挡减伤"
				rep.Rounds = append(rep.Rounds, mr)
				if drLeft > 0 {
					drLeft--
				} // 每次怪物攻击消耗一回合减伤
			}
			if heroHP <= 0 || totalMonHP() <= 0 {
				break
			}
		}
	}
	if oneShot {
		rep.Result = "win"
		rep.OneShot = true
	} else if totalMonHP() <= 0 {
		rep.Result = "win"
	} else {
		rep.Result = "lose"
	}
	// v1.9.27 战斗结束血量：保留余量（战败保底 1 点，防 0 血软锁；用回血道具恢复）
	rep.HeroHPEnd = round2(math.Max(1, heroHP))
	if oneShot {
		rep.Seconds = 0 // 秒杀不消耗战斗时长
	} else {
		rep.Seconds = round2(float64(len(rep.Rounds)) / 2.0 * tick)
	}

	if rep.Result == "win" {
		mult := 1.0
		if req.Elite == "elite" {
			mult = 2
		}
		if req.Elite == "rareElite" {
			mult = 3
		}
		// v1.9.46 大地图首领：前端按怪物档位传 elite="boss"，与 isBoss 怪物同等奖励倍率
		if req.Elite == "boss" || mon.IsBoss {
			mult = 5
		}
		// 等级系数：Lv1 = 1.0 倍，之后每级 +20%（原公式 Lv1 只有 0.1 倍，前期收益几乎为零）
		lvMult := 1 + 0.2*float64(req.MonsterLv-1)
		// v1.9.36 群怪：奖励按成员累加，2 只以上再 ×1.15 群战加成（单怪数值不变）
		expSum, goldSum := 0.0, 0.0
		for i := range mems {
			expSum += mems[i].def.Exp
			goldSum += mems[i].def.Gold
		}
		if len(mems) > 1 {
			expSum *= 1.15
			goldSum *= 1.15
		}
		rep.Exp = int(expSum * lvMult * mult)
		rep.Gold = int(goldSum * lvMult * mult)
		rep.Drops = rollDrops(req, mon, rng)
		// 群怪掉落：每多一只怪 30% 概率额外 roll 一件（用该成员的掉落定义）
		for extra := 1; extra < len(mems); extra++ {
			if rng.Float64() < 0.3 {
				rep.Drops = append(rep.Drops, rollDrops(req, mems[extra].def, rng)...)
			}
		}
		rep.Log = "服务端权威结算：胜利，经验/金币/掉落已入账"
		if oneShot {
			rep.Log = "一击必杀：怪物血量低于普攻保底伤害，跳过战斗直接结算"
		}
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
	// v1.9.43 安全护栏：Boss 掉落循环加 10 件上限（equipChanceBoss 配 ≥1.0 会死循环）
	for len(out) < 10 && rng.Float64() < chance {
		slot := rollSlot(c, rng)
		q := rollQuality(rng) // v1.9.42 品质权重 roll（高阶品质递减，可出彩虹/暗影/混沌）
		base := c.Equipment.BaseStatsBySlot[slot]
		stats := map[string]float64{}
		for k, v := range base {
			stats[k] = round2(v * (1 + rng.Float64()*0.3))
		}
		out = append(out, EquipItem{
			ID: mon.ID + "_" + slot + "_" + fmtID(rng.Int63()), Slot: slot, Quality: q, Enhance: 0, Stats: stats,
			// v1.9.38 武器分系：武器按玩家职业所属系生成（战士系/法师系互不通用）
			ClassID: weaponClassIf(slot, req.Avatar.ClassID),
		})
		if !mon.IsBoss {
			break
		}
	}
	return out
}

// RollChestEquip 宝箱按 tier roll 一件装备（v1.9.35 开箱详细掉落）。
// 概率与品质上限内置于表：wood 12%/精良，silver 25%/稀有，gold 45%/史诗，
// v1.9.42 新增 diamond 70%/彩虹、crystal 100%/混沌。
// 返回 (装备, 是否命中)。复用战斗掉落同款属性生成（基值 ×0.0~1.3 随机浮动）。
// v1.9.38：classID 用于武器分系——开出的武器按开箱玩家职业所属系生成。
func RollChestEquip(classID, tier string, rng *rand.Rand) (EquipItem, bool) {
	c := C()
	chance := map[string]float64{"wood": 0.12, "silver": 0.25, "gold": 0.45, "diamond": 0.70, "crystal": 1.0}[tier]
	if chance == 0 {
		chance = 0.12
	}
	if rng.Float64() >= chance {
		return EquipItem{}, false
	}
	qCap := map[string]int{"wood": 1, "silver": 2, "gold": 3, "diamond": 5, "crystal": 7}[tier]
	if qCap >= len(c.Equipment.Quality) {
		qCap = len(c.Equipment.Quality) - 1
	}
	slot := rollSlot(c, rng)
	q := rng.Intn(qCap + 1)
	if tier == "crystal" {
		// 水晶箱品质下限彩虹（5）：在 5..qCap 内偏高端 roll
		q = qCap - rng.Intn(3)
		if q < 5 {
			q = 5
		}
	}
	base := c.Equipment.BaseStatsBySlot[slot]
	stats := map[string]float64{}
	for k, v := range base {
		stats[k] = round2(v * (1 + rng.Float64()*0.3))
	}
	return EquipItem{
		ID: "chest_" + slot + "_" + fmtID(rng.Int63()), Slot: slot, Quality: q, Enhance: 0, Stats: stats,
		ClassID: weaponClassIf(slot, classID),
	}, true
}

// rollQuality 战斗掉落品质权重（v1.9.42 品质 8 档）：普通38/精良28/稀有18/史诗10/传说4.2/彩虹1.2/暗影0.5/混沌0.1（合计100）。
func rollQuality(rng *rand.Rand) int {
	r := rng.Float64() * 100
	switch {
	case r < 38:
		return 0
	case r < 66:
		return 1
	case r < 84:
		return 2
	case r < 94:
		return 3
	case r < 98.2:
		return 4
	case r < 99.4:
		return 5
	case r < 99.9:
		return 6
	default:
		return 7
	}
}

// weaponClassIf 仅武器槽返回职业所属武器系，其余槽位返回空（通用）。
func weaponClassIf(slot, classID string) string {
	if slot == "weapon" {
		return WeaponClassFor(classID)
	}
	return ""
}

// rollSlot 按配置加权抽取掉落槽位（v1.9.30）：economy.drop.slotWeights 未配置时四槽均等。
// 默认配置 weapon=0.4（其余三槽均分余量）——武器是战斗核心产出，大幅提高武器占比。
func rollSlot(c *Config, rng *rand.Rand) string {
	w, ok := c.Economy.Drop["slotWeights"].(map[string]any)
	if !ok || len(w) == 0 {
		slots := c.Equipment.Slots
		return slots[rng.Intn(len(slots))]
	}
	weights := make([]float64, len(c.Equipment.Slots))
	total := 0.0
	for i, sl := range c.Equipment.Slots {
		weights[i] = num(w[sl])
		total += weights[i]
	}
	if total <= 0 {
		slots := c.Equipment.Slots
		return slots[rng.Intn(len(slots))]
	}
	r := rng.Float64() * total
	for i, sl := range c.Equipment.Slots {
		r -= weights[i]
		if r <= 0 {
			return sl
		}
	}
	return c.Equipment.Slots[len(c.Equipment.Slots)-1]
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
