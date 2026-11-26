package game

import "math"

// Stats 是角色最终属性。服务端权威计算，客户端只接收结果做表现。
type Stats struct {
	HP    float64 `json:"hp"`
	ATK   float64 `json:"atk"`
	DEF   float64 `json:"def"`
	Crit  float64 `json:"crit"`
	Spd   float64 `json:"spd"`
	Power float64 `json:"power"` // 展示用战力
}

// EquipItem 是一件装备的存档表示（对应 equipment 表一行）。
// v1.9.38：武器分系——ClassID 仅武器使用（"warrior"/"mage" 系），空=通用（防具/饰品/老档）。
type EquipItem struct {
	ID      string             `json:"id"`
	Slot    string             `json:"slot"`
	Quality int                `json:"quality"`
	Enhance int                `json:"enhance"`
	Stats   map[string]float64 `json:"stats"`
	ClassID string             `json:"classId,omitempty"` // 武器系标记（战士系/法师系）
}

// WeaponClassFor 职业 → 所属武器系（v1.9.38）。未配置/未知职业归战士系。
func WeaponClassFor(classID string) string {
	for wc, ids := range C().Equipment.WeaponClasses {
		if wc == "_note" { continue }
		for _, id := range ids {
			if id == classID { return wc }
		}
	}
	return "warrior"
}

// WeaponName 武器按系+品质取名（v1.9.38）；非武器或未配置回退品质+槽位名。
func WeaponName(classID string, quality int, slot string) string {
	if slot != "weapon" || classID == "" { return "" }
	names, ok := C().Equipment.WeaponNames[classID]
	if !ok || len(names) == 0 { return "" }
	if quality < 0 { quality = 0 }
	if quality >= len(names) { quality = len(names) - 1 }
	return names[quality]
}

// WeaponClassCN 武器系中文名。
func WeaponClassCN(wc string) string {
	if wc == "mage" { return "法师" }
	return "战士"
}

// EquipPower 单件装备的战力贡献（v1.9.28，随 /api/stats/calc 返回）。
type EquipPower struct {
	ID    string  `json:"id"`
	Power float64 `json:"power"`
}

// Avatar 是参与属性计算的角色最小输入。
type Avatar struct {
	Level   int         `json:"level"`
	ClassID string      `json:"classId"`
	Equip   []EquipItem `json:"equip"`
}

// CalcStats 按 职业系数 × 等级成长 × 装备 计算最终属性。
// 转职时传入新的 classId 即自动完成装备换算（classCoefficient），无需改装备数据。
func CalcStats(a Avatar) Stats {
	c := C()
	base := c.Classes.BaseStats
	g := c.Classes.LevelGrowth
	cl, ok := c.ClassByID(a.ClassID)
	if !ok {
		cl = c.Classes.Classes[0]
	}

	lv := a.Level - 1
	if lv < 0 {
		lv = 0
	}
	st := Stats{
		HP:   (base["hp"] + num(g["hpPerLevel"])*float64(lv)) * cl.Bonus["hp"],
		ATK:  (base["atk"] + num(g["atkPerLevel"])*float64(lv)) * cl.Bonus["atk"],
		DEF:  (base["def"] + num(g["defPerLevel"])*float64(lv)) * cl.Bonus["def"],
		Crit: base["crit"] * cl.Bonus["crit"],
		Spd:  base["speed"],
	}

	// 装备加成：品级系数 × 强化增益 × 职业换算系数
	coef := coefOf(c, a.ClassID)
	enh := c.Equipment.Enhance
	gainPerLv := num(enh["statGainPerLevel"])
	qualities := c.Equipment.Quality
	for _, it := range a.Equip {
		applyEquip(&st, c, classID2EquipParams{coef: coef, gainPerLv: gainPerLv, qualities: qualities}, it)
	}
	st.Power = PowerOf(st)
	return st
}

// equipParams 抽出装备折算所需的配置快照，供 CalcStats 与 EquipContribution 共用。
type classID2EquipParams struct {
	coef       map[string]any
	gainPerLv  float64
	qualities  []QualityDef
}

// applyEquip 把一件装备的属性贡献累加到 st 上（品质/强化/职业系数全套规则）。
func applyEquip(st *Stats, c *Config, p classID2EquipParams, it EquipItem) {
	qmult := 1.0
	for _, q := range p.qualities {
		if q.ID == it.Quality {
			qmult = q.Mult
			break
		}
	}
	boost := 1 + p.gainPerLv*float64(it.Enhance)
	for k, v := range it.Stats {
		cf := 1.0
		if _, exists := p.coef[k]; exists {
			cf = num(p.coef[k])
		}
		switch k {
		case "hp":
			st.HP += v * qmult * boost * cf
		case "atk":
			st.ATK += v * qmult * boost * cf
		case "def":
			st.DEF += v * qmult * boost * cf
		case "crit":
			st.Crit += v * qmult * boost
		case "speed":
			st.Spd += v * qmult * boost
		}
	}
}

// PowerOf 全局战力公式（与 CalcStats 保持同一口径）。
func PowerOf(st Stats) float64 {
	return math.Round(st.ATK*3 + st.DEF*2 + st.HP*0.4 + st.Crit*800)
}

// EquipContribution v1.9.28：单件装备对指定职业的战力贡献（含品质/强化/职业换算系数）。
// 供装备列表、强化工坊、仓库显示"战力 +N"。
func EquipContribution(classID string, it EquipItem) float64 {
	c := C()
	st := Stats{}
	applyEquip(&st, c, classID2EquipParams{
		coef:      coefOf(c, classID),
		gainPerLv: num(c.Equipment.Enhance["statGainPerLevel"]),
		qualities: c.Equipment.Quality,
	}, it)
	return PowerOf(st)
}

// TransferEquip 转职装备自动换算（v1.9.30 双向修正）：属性按 新职业系数/旧职业系数 的比值折算。
// 旧实现只乘新职业系数，战士→法师→战士来回互转会让武器属性持续膨胀（20→23→26…），
// 修复后互转数值精确还原，武器真正跨职业互通。
func TransferEquip(items []EquipItem, fromClassID, toClassID string) []EquipItem {
	c := C()
	coefFrom := coefOf(c, fromClassID)
	coefTo := coefOf(c, toClassID)
	out := make([]EquipItem, len(items))
	copy(out, items)
	for i := range out {
		out[i].Stats = map[string]float64{}
		for k, v := range items[i].Stats {
			ratio := 1.0
			if fv, ok := coefFrom[k]; ok {
				if tv, ok2 := coefTo[k]; ok2 && num(fv) != 0 {
					ratio = num(tv) / num(fv)
				}
			} else if tv, ok2 := coefTo[k]; ok2 {
				ratio = num(tv)
			}
			out[i].Stats[k] = round2(v * ratio)
		}
	}
	return out
}

// ExpNeeded 计算升到下一级所需经验。
func ExpNeeded(level int) int {
	g := C().Classes.LevelGrowth
	return int(math.Round(num(g["expBase"]) * math.Pow(num(g["expGrowth"]), float64(level-1))))
}

func num(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case int64:
		return float64(x)
	default:
		return 0
	}
}

// coefOf 取职业装备换算系数表。配置里含 _note 说明字段，故外层用 any 存储。
func coefOf(c *Config, classID string) map[string]any {
	raw, ok := c.Equipment.ClassCoefficient[classID]
	if !ok {
		return map[string]any{"atk": 1.0, "def": 1.0, "hp": 1.0}
	}
	if m, ok := raw.(map[string]any); ok {
		return m
	}
	return map[string]any{"atk": 1.0, "def": 1.0, "hp": 1.0}
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }
