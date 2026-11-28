// 点位刷新状态机：固定点位 + 随机刷新（对应地图设计 v0.8 §3 / 表10）。
// 怪为世界实例共享；宝箱为玩家独立实例（按 userId+nodeId 分键）。
// 冷却 base×(1±jitter)、内容 tier roll、保底 8 次、同时 ACTIVE ≤ 密度上限。
// 所有参数来自 worlds.json respawn / economy.json drop，本包不硬编码数值。
package game

import (
	"math/rand"
	"strings"
	"sync"
	"time"
)

// SpotState 单个点位的运行时状态。
type SpotState struct {
	NodeID     string `json:"nodeId"`
	Type       string `json:"type"`
	Tier       int    `json:"tier"`
	X          int    `json:"x"`
	Y          int    `json:"y"`
	Active     bool   `json:"active"`
	ReadyAt    int64  `json:"readyAt"`    // 下次可刷新的服务端时间戳（秒）
	ContentID  string `json:"contentId"`  // 当前 ACTIVE 的内容（怪物id / 宝箱tier）
	BossMonster string `json:"bossMonster,omitempty"` // GATE/BOSS 点位绑定的怪物 id
	RollCount  int    `json:"rollCount"`  // 连续未出稀有的次数（保底用）
	To         string `json:"to,omitempty"`
	ReqLevel   int    `json:"requireLevel,omitempty"`
	RequireUnlocked bool `json:"requireUnlocked,omitempty"` // GATE：目标世界需已解锁（击败本世界首领开启）
	Level      int    `json:"level,omitempty"` // v2.0 点位独立等级（BOSS 用）
	// v1.9.46：该点位坐标是否只是兜底值（首领点位由客户端在可走区域随机落点）
	RandomPos bool     `json:"randomPos,omitempty"`
	Group     []string `json:"group,omitempty"` // v1.9.36 群怪模版：点位怪物组合（monster id，可重复），战斗/贴图以此为准
}

// SpawnManager 全部世界点位的运行时状态，线程安全。
type SpawnManager struct {
	mu     sync.RWMutex
	spots  map[string]*SpotState          // "worldId/nodeId" -> state（怪物世界实例）
	chests map[string]*SpotState          // "userId/worldId/nodeId" -> state（宝箱玩家实例）
	rolls  map[string]int                 // 保底计数键
	rand   *rand.Rand
}

var spawnMgr *SpawnManager
var spawnOnce sync.Once

func InitSpawns() {
	spawnOnce.Do(func() {
		spawnMgr = &SpawnManager{
			spots:  map[string]*SpotState{},
			chests: map[string]*SpotState{},
			rolls:  map[string]int{},
			rand:   rand.New(rand.NewSource(time.Now().UnixNano())),
		}
		now := ServerNow()
		c := C()
		for _, w := range c.Worlds.Worlds {
			for _, s := range w.Spawns {
				if s.Type == "CHEST" || s.Type == "POTION" { continue } // 宝箱/草药走玩家实例，按需创建
				key := w.ID + "/" + s.ID
				content := s.BossMonster
				if s.Content != "" { content = s.Content } // v1.9.31 FUNC 点位：content 直传前端
				spawnMgr.spots[key] = &SpotState{
					NodeID: s.ID, Type: s.Type, Tier: s.Tier, X: s.X, Y: s.Y,
					To: s.To, ReqLevel: s.RequireLevel, RequireUnlocked: s.RequireUnlocked, Level: s.Level, BossMonster: s.BossMonster,
					RandomPos: s.RandomPos,
					Active: true, ReadyAt: now, ContentID: content,
				}
				// v1.9.36 初始即 roll 群怪组合（BOSS 单首领 / ELITE 主怪+小怪 / MOB 随机 1-3）
				if s.Type == "MOB" || s.Type == "ELITE" || s.Type == "BOSS" {
					spawnMgr.spots[key].Group = spawnMgr.rollGroup(s.Type, s.Tier, s.BossMonster)
				}
			}
		}
	})
}

func SM() *SpawnManager { InitSpawns(); return spawnMgr }

func numOf(m map[string]any, k string, def float64) float64 {
	if v, ok := m[k]; ok {
		switch n := v.(type) {
		case float64: return n
		case int: return float64(n)
		}
	}
	return def
}

// cooldownFor 按点位类型取冷却 base×(1±jitter)。
func (m *SpawnManager) cooldownFor(typ string) int64 {
	r := C().Worlds.Respawn
	var base float64
	switch typ {
	case "ELITE", "BOSS": base = numOf(r, "eliteBaseSec", 180)
	case "CHEST": base = numOf(r, "chestBaseSec", 300)
	case "POTION": base = numOf(r, "potionBaseSec", 120) // v1.9.27 草药 respawn
	default: base = numOf(r, "mobBaseSec", 60)
	}
	j := numOf(r, "jitter", 0.2)
	factor := 1 + (m.rand.Float64()*2-1)*j
	return int64(base * factor)
}

// rollContent 按 tier 权重 roll 内容，含保底（连续 N 次未出稀有则强制稀有）。
// 宝箱（需求文档表22 / 地图设计3.3）：木60% / 银30% / 金10%（tier 1/2/3）。
// 怪物（表10）：普通70% / 精英25% / 稀有5%。保底8次。
func (m *SpawnManager) rollContent(spotType string, pityKey string) (tier int) {
	m.rolls[pityKey]++
	pity := 8
	r := m.rand.Float64()
	if m.rolls[pityKey] >= pity { tier = 3; m.rolls[pityKey] = 0; return }
	if spotType == "CHEST" {
		// v1.9.42：木52 / 银28 / 金12 / 钻2.5 / 水晶1.5（新增钻石箱/水晶箱，高档位总出现率 10%→16%）
		switch {
		case r < 0.015:
			tier = 5 // 水晶
			m.rolls[pityKey] = 0
		case r < 0.04:
			tier = 4 // 钻石
		case r < 0.16:
			tier = 3 // 金
		case r < 0.44:
			tier = 2 // 银
		default:
			tier = 1 // 木
		}
		return
	}
	switch {
	case r < 0.05:
		tier = 3
		m.rolls[pityKey] = 0
	case r < 0.30:
		tier = 2
	default:
		tier = 1
	}
	return
}

// rollGroup v1.9.36 群怪模版：按点位类型/tier roll 怪物组合。
// v1.9.37 怪物池扩充（衍生怪随 tier 渐进混入）：1=史莱姆为主/少量毒液史莱姆，
// 2=史莱姆/毒液史莱姆/骷髅，3+=骷髅/骷髅弓手/蝙蝠/血蝙蝠。
// MOB：45% 单怪 / 35% 双怪 / 20% 三怪（随机组合，可混编）；
// ELITE：主怪（点位内容）60% 单出 / 25% 带 1 小怪 / 15% 带 2 小怪；
// BOSS：固定单首领。组合在点位激活时 roll 并存入 SpotState.Group，
// 遭遇贴图与战斗结算使用同一组合（弹窗贴图与实际战斗一致）。
func (m *SpawnManager) rollGroup(spotType string, tier int, bossMonster string) []string {
	pool := []string{"slime"}
	if tier <= 1 {
		// v1.9.37：tier1 以史莱姆为主，30% 混入毒液史莱姆（同模型衍生怪）
		pool = []string{"slime", "slime", "slime", "slime_toxic"}
	} else if tier == 2 {
		pool = []string{"slime", "slime", "slime_toxic", "skeleton", "skeleton"}
	} else {
		pool = []string{"skeleton", "skeleton", "skeleton_archer", "bat", "bat_crimson"}
	}
	pick := func() string { return pool[m.rand.Intn(len(pool))] }
	switch spotType {
	case "BOSS":
		if bossMonster != "" { return []string{bossMonster} }
		return []string{"boss_beetle"}
	case "ELITE":
		g := []string{bossMonster}
		if g[0] == "" { g[0] = pick() }
		r := m.rand.Float64()
		if r < 0.25 { g = append(g, pick()) } else if r < 0.40 { g = append(g, pick(), pick()) }
		return g
	default: // MOB
		r := m.rand.Float64()
		n := 1
		switch {
		case r < 0.20: n = 3
		case r < 0.55: n = 2
		}
		g := make([]string, 0, n)
		for i := 0; i < n; i++ { g = append(g, pick()) }
		return g
	}
}

// ActiveSpots 返回某世界当前 ACTIVE 的点位（供客户端渲染）；刷新到期的点位自动复活。
// monster 世界共享；chest 玩家独立（传 userId 时并入该玩家的宝箱实例）。
func (m *SpawnManager) ActiveSpots(worldID, userID string) []SpotState {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := ServerNow()
	capN := int(numOf(C().Worlds.Respawn, "activeDensityCap", 8))
	out := []SpotState{}
	active := 0
	for key, s := range m.spots {
		if key[:len(worldID)+1] != worldID+"/" { continue }
		if !s.Active && now >= s.ReadyAt {
			s.Active = true
			s.ContentID = s.BossMonster
			s.Group = m.rollGroup(s.Type, s.Tier, s.BossMonster) // v1.9.36 复活时重 roll 组合
		}
		if s.Active {
			out = append(out, *s)
			// v1.9.46：首领不占密度额度——每图必须稳定出现一个首领，
			// 否则 8 点上限会在 12+ 怪物点位里随机剔除，导致首领时有时无。
			if s.Type == "MOB" || s.Type == "ELITE" { active++ }
		}
	}
	// 密度上限：超出则把多余的怪点临时转为冷却（不删除点位）；首领不参与淘汰
	if active > capN {
		extra := active - capN
		for i := len(out)-1; i >= 0 && extra > 0; i-- {
			if out[i].Type == "MOB" || out[i].Type == "ELITE" {
				if k, ok := m.spots[worldID+"/"+out[i].NodeID]; ok {
					k.Active = false
					k.ReadyAt = now + m.cooldownFor(k.Type)
				}
				out = append(out[:i], out[i+1:]...)
				extra--
			}
		}
	}
	// 并入该玩家的宝箱实例（首次查询时按世界配置为玩家创建 CHEST 点位）
	// v1.9.27：草药（POTION）点位同理为玩家实例，采集后进入冷却（potionBaseSec）自动复活
	if userID != "" {
		for _, wd := range C().Worlds.Worlds {
			if wd.ID != worldID { continue }
			for _, sd := range wd.Spawns {
				if sd.Type != "CHEST" && sd.Type != "POTION" { continue }
				key := userID + "/" + worldID + "/" + sd.ID
				s, ok := m.chests[key]
				if !ok {
					// 首次创建按服务端权威 roll 出档位（木60/银30/金10），不取配置固定值
					tier := 1
					if sd.Type == "CHEST" { tier = m.rollContent("CHEST", key) }
					s = &SpotState{NodeID: sd.ID, Type: sd.Type, Tier: tier, X: sd.X, Y: sd.Y,
						Active: true, ReadyAt: now, ContentID: "chest"}
					if sd.Type == "POTION" { s.ContentID = "potion" }
					m.chests[key] = s
				} else if !s.Active && now >= s.ReadyAt {
					s.Active = true
					if s.Type == "CHEST" { s.Tier = m.rollContent("CHEST", key) }
				}
				if s.Active { out = append(out, *s) }
			}
		}
	}
	return out
}

// KillMonster 击败怪物 → 点位进入冷却，返回是否成功。
func (m *SpawnManager) KillMonster(worldID, nodeID string) bool {
	m.mu.Lock(); defer m.mu.Unlock()
	k := worldID + "/" + nodeID
	s, ok := m.spots[k]
	if !ok || !s.Active { return false }
	s.Active = false
	s.ReadyAt = ServerNow() + m.cooldownFor(s.Type)
	return true
}

// MonsterGroup v1.9.36 查询怪物点位当前群怪组合（战斗结算权威来源）。
// 点位必须 ACTIVE；遭遇贴图与战斗结算共用同一组合，保证所见即所战。
func (m *SpawnManager) MonsterGroup(worldID, nodeID string) ([]string, bool) {
	m.mu.RLock(); defer m.mu.RUnlock()
	s, ok := m.spots[worldID+"/"+nodeID]
	if !ok || !s.Active || len(s.Group) == 0 { return nil, false }
	return s.Group, true
}

// EnsureChest 确保玩家在某宝箱点位有实例（不存在则创建 ACTIVE）。
func (m *SpawnManager) EnsureChest(userID, worldID, nodeID string, tier int, x, y int) SpotState {
	m.mu.Lock(); defer m.mu.Unlock()
	key := userID + "/" + worldID + "/" + nodeID
	s, ok := m.chests[key]
	if !ok {
		// 服务端权威 roll 档位（忽略传入 tier，防客户端伪造）
		rolled := m.rollContent("CHEST", key)
		s = &SpotState{NodeID: nodeID, Type: "CHEST", Tier: rolled, X: x, Y: y, Active: true, ReadyAt: ServerNow(), ContentID: "chest"}
		m.chests[key] = s
	} else if !s.Active && ServerNow() >= s.ReadyAt {
		s.Active = true
		s.Tier = m.rollContent("CHEST", key)
	}
	return *s
}

// OpenChest 开箱成功 → 宝箱进入冷却；返回刷新后的状态。
// v1.9.17 冷却到期时在开箱请求内即时重新激活（不再依赖下一次 spawn 轮询），
// 配合 chestBaseSec=0 即"开完立即可再开"。
func (m *SpawnManager) OpenChest(userID, worldID, nodeID string) (SpotState, bool) {
	m.mu.Lock(); defer m.mu.Unlock()
	key := userID + "/" + worldID + "/" + nodeID
	isDrop := strings.HasPrefix(nodeID, "drop_")
	s, ok := m.chests[key]
	if !ok {
		// v1.9.45：掉落箱必须先在 /api/chest/drop 登记（服务端掷档）；未登记一律拒绝，
		// 避免客户端伪造 drop_ 前缀任意刷箱、或重复 roll 赌高价值档。
		if isDrop {
			return SpotState{}, false
		}
		if !m.configHasChest(worldID, nodeID) {
			return SpotState{}, false
		}
		tier := m.rollContent("CHEST", key)
		s = &SpotState{NodeID: nodeID, Type: "CHEST", Tier: tier, Active: true,
			ReadyAt: ServerNow(), ContentID: "chest"}
		m.chests[key] = s
	}
	if !s.Active {
		if ServerNow() < s.ReadyAt { return SpotState{}, false }
		s.Active = true
		s.Tier = m.rollContent("CHEST", key)
	}
	s.Active = false
	s.ReadyAt = ServerNow() + m.cooldownFor("CHEST")
	out := *s
	// v1.9.45：掉落箱一次性——开完即销毁，同名 nodeId 不可重复开启
	if isDrop { delete(m.chests, key) }
	return out, true
}

// RegisterDropChest 怪物掉落宝箱登记（v1.9.45）。
// 服务端按 economy.drop.monsterChest 权重权威掷档位并建实例，客户端不再参与概率；
// 同一 nodeId 只允许登记一次（防重复 roll 赌档），同玩家未开启的掉落箱上限 12 个（防批量刷箱）。
// v1.9.46：kind 区分 normal/elite/boss 三档怪物，档次越高高品质箱概率越高。
func (m *SpawnManager) RegisterDropChest(userID, worldID, nodeID, kind string) (SpotState, bool) {
	m.mu.Lock(); defer m.mu.Unlock()
	key := userID + "/" + worldID + "/" + nodeID
	if _, exists := m.chests[key]; exists { return SpotState{}, false }
	n := 0
	for k, st := range m.chests {
		if strings.HasPrefix(k, userID+"/") && st.Active && strings.HasPrefix(st.NodeID, "drop_") { n++ }
		if n >= 12 { return SpotState{}, false }
	}
	s := &SpotState{NodeID: nodeID, Type: "CHEST", Tier: m.rollDropTier(kind), Active: true,
		ReadyAt: ServerNow(), ContentID: "chest"}
	m.chests[key] = s
	return *s, true
}

// DropKindOf 归一化怪物档次（v1.9.46）：只认 elite/boss，其余一律 normal。
func DropKindOf(kind string) string {
	switch kind {
	case "elite", "boss":
		return kind
	}
	return "normal"
}

// tierIndexByName 箱子品质名 → 档位序号（1木 2银 3金 4钻石 5水晶）。
// v1.9.57 供 monsterChest 的 floor（保底品质）解析用；未知/空名按木箱兜底。
func tierIndexByName(name string) int {
	switch name {
	case "silver":
		return 2
	case "gold":
		return 3
	case "diamond":
		return 4
	case "crystal":
		return 5
	}
	return 1
}

// rollDropTier 按 monsterChest 权重掷档位（1木 2银 3金 4钻石 5水晶）。
// 保底（rollContent 的 8 次计数）对掉落箱不适用：每次掉落都是独立的一次抽取。
// v1.9.46：三档权重来自配置，每档内部递减且不设 0；缺配置时按 normal 兜底。
// v1.9.57：每档可配 floor（保底品质）。低于 floor 的权重在掷档前全部并入 floor，
// 即「保底箱概率 = 自身权重 + 所有更低品质权重之和」——精英怪必出银箱起、首领必出金箱起。
func (m *SpawnManager) rollDropTier(kind string) int {
	prof := DropKindOf(kind)
	raw, _ := C().Economy.Drop["monsterChest"].(map[string]any)
	w, _ := raw[prof].(map[string]any)
	if w == nil { // 老配置只有 normal/boss：elite 回退到 normal
		w, _ = raw["normal"].(map[string]any)
	}
	names := []string{"wood", "silver", "gold", "diamond", "crystal"}
	vals := make([]float64, len(names))
	total := 0.0
	for i, n := range names {
		vals[i] = numOf(w, n, 0)
		total += vals[i]
	}
	if total <= 0 { // 配置缺失兜底：普通怪木箱、精英怪银箱、首领金箱
		switch prof {
		case "elite":
			return 2
		case "boss":
			return 3
		}
		return 1
	}
	// 保底档归一：低于 floor 的权重并入 floor，这些档位不再单独产出
	floor := 1 // 缺省按木箱兜底
	if fs, ok := w["floor"].(string); ok && fs != "" {
		floor = tierIndexByName(fs)
	}
	if floor > 1 {
		merge := 0.0
		for i := 0; i < floor-1; i++ {
			merge += vals[i]
			vals[i] = 0
		}
		vals[floor-1] += merge
	}
	r := m.rand.Float64() * total
	acc := 0.0
	for i, v := range vals {
		if v <= 0 {
			continue
		}
		acc += v
		if r < acc { return i + 1 }
	}
	return 1
}

// WorldRewardMult 地图产出系数（v1.9.46）：宝箱经验/金币/钻石/材料/药水按此放大。
// 配置为 1.0 / 1.2 / 1.44 …（每张图比上一张多 20%）；未配置或非法值按 1.0。
func WorldRewardMult(worldID string) float64 {
	for _, w := range C().Worlds.Worlds {
		if w.ID != worldID { continue }
		if w.RewardMult > 0 { return w.RewardMult }
		return 1
	}
	return 1
}

// configHasChest 判断该世界配置里是否存在指定 ID 的宝箱点位。
func (m *SpawnManager) configHasChest(worldID, nodeID string) bool {
	return m.configHasType(worldID, nodeID, "CHEST")
}

// configHasType 判断该世界配置里是否存在指定类型、指定 ID 的点位（v1.9.27 草药校验用）。
func (m *SpawnManager) configHasType(worldID, nodeID, typ string) bool {
	for _, w := range C().Worlds.Worlds {
		if w.ID != worldID { continue }
		for _, sd := range w.Spawns {
			if sd.Type == typ && sd.ID == nodeID { return true }
		}
	}
	return false
}

// PickPotion 采集草药：确保玩家实例存在且 ACTIVE → 采集成功进入冷却。
// 返回刷新后的点位状态；不可采（冷却中/不存在）返回 false。
func (m *SpawnManager) PickPotion(userID, worldID, nodeID string) (SpotState, bool) {
	m.mu.Lock(); defer m.mu.Unlock()
	if !m.configHasType(worldID, nodeID, "POTION") {
		return SpotState{}, false
	}
	key := userID + "/" + worldID + "/" + nodeID
	s, ok := m.chests[key]
	if !ok {
		s = &SpotState{NodeID: nodeID, Type: "POTION", Tier: 1, Active: true,
			ReadyAt: ServerNow(), ContentID: "potion"}
		m.chests[key] = s
	}
	if !s.Active {
		if ServerNow() < s.ReadyAt { return SpotState{}, false }
		s.Active = true
	}
	s.Active = false
	s.ReadyAt = ServerNow() + m.cooldownFor("POTION")
	return *s, true
}
