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
type EquipItem struct {
	ID      string             `json:"id"`
	Slot    string             `json:"slot"`
	Quality int                `json:"quality"`
	Enhance int                `json:"enhance"`
	Stats   map[string]float64 `json:"stats"`
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
		qmult := 1.0
		for _, q := range qualities {
			if q.ID == it.Quality {
				qmult = q.Mult
				break
			}
		}
		boost := 1 + gainPerLv*float64(it.Enhance)
		for k, v := range it.Stats {
			cf := 1.0
			if _, exists := coef[k]; exists {
				cf = num(coef[k])
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
	st.Power = math.Round(st.ATK*3 + st.DEF*2 + st.HP*0.4 + st.Crit*800)
	return st
}

// TransferEquip 转职装备自动换算：返回按新职业系数折算后的属性。
// 这是纯函数，转职时统一走这里，避免规则散落各处导致不一致（交互矩阵 bug 项）。
func TransferEquip(items []EquipItem, newClassID string) []EquipItem {
	c := C()
	coef := coefOf(c, newClassID)
	out := make([]EquipItem, len(items))
	copy(out, items)
	for i := range out {
		out[i].Stats = map[string]float64{}
		for k, v := range items[i].Stats {
			cf := 1.0
			if _, exists := coef[k]; exists {
				cf = num(coef[k])
			}
			out[i].Stats[k] = round2(v * cf)
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
