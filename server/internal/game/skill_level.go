package game

// v1.9.56 技能等级系统（服务端权威）
//
// 规则：每个已解锁技能从 1 级起，每升 1 级技能效果 +8%（power / 治疗量 / 减伤比例同乘），
// 升级消耗钻石 = baseGem + stepGem×(当前等级-1)，等级上限 maxLevel。
// 数值全部来自 configs/economy.json 的 skillUpgrade 段；缺配置时用下面默认值兜底，
// 保证老板子/老存档不会因漏配而报错。

const (
	defSkillPerLevel = 0.08 // 每级效果增幅
	defSkillMaxLevel = 20   // 等级上限
	defSkillBaseGem  = 60   // 1→2 级消耗
	defSkillStepGem  = 30   // 每级递增
)

// skillUpgradeCfg 读取 skillUpgrade 配置段（float 用 float64，int 用 int 转换）。
func skillUpgradeCfg() map[string]any {
	if c := C(); c != nil {
		return c.Economy.SkillUpgrade
	}
	return nil
}

func skillCfgFloat(key string, def float64) float64 {
	if cfg := skillUpgradeCfg(); cfg != nil {
		if v, ok := cfg[key].(float64); ok && v > 0 {
			return v
		}
	}
	return def
}

func skillCfgInt(key string, def int) int {
	if cfg := skillUpgradeCfg(); cfg != nil {
		if v, ok := cfg[key].(float64); ok && v > 0 {
			return int(v)
		}
	}
	return def
}

// SkillMaxLevel 技能等级上限。
func SkillMaxLevel() int { return skillCfgInt("maxLevel", defSkillMaxLevel) }

// SkillEffectMult 技能效果倍率：1 级 = 1.0，每级 +8%（线性叠加）。
// 非法等级（<=0）按 1 级处理，老存档没有等级字段时同样走这里。
func SkillEffectMult(level int) float64 {
	if level < 1 {
		level = 1
	}
	if max := SkillMaxLevel(); level > max {
		level = max
	}
	per := skillCfgFloat("perLevel", defSkillPerLevel)
	return 1 + per*float64(level-1)
}

// SkillUpgradeCost 从 level 升到 level+1 需要的钻石；已满级返回 0。
func SkillUpgradeCost(level int) int {
	if level < 1 {
		level = 1
	}
	if level >= SkillMaxLevel() {
		return 0
	}
	base := skillCfgInt("baseGem", defSkillBaseGem)
	step := skillCfgInt("stepGem", defSkillStepGem)
	return base + step*(level-1)
}
