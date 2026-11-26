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
	Group      []string `json:"group,omitempty"` // v1.9.36 群怪模版：点位怪物组合（monster id，可重复），战斗/贴图以此为准
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
					To: s.To, ReqLevel: s.RequireLevel, BossMonster: s.BossMonster,
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
		// 木60 / 银30 / 金10
		switch {
		case r < 0.10:
			tier = 3 // 金
			m.rolls[pityKey] = 0
		case r < 0.40:
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
			if s.Type == "MOB" || s.Type == "ELITE" || s.Type == "BOSS" { active++ }
		}
	}
	// 密度上限：超出则把多余的怪点临时转为冷却（不删除点位）
	if active > capN {
		extra := active - capN
		for i := len(out)-1; i >= 0 && extra > 0; i-- {
			if out[i].Type == "MOB" || out[i].Type == "ELITE" || out[i].Type == "BOSS" {
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
	s, ok := m.chests[key]
	if !ok {
		// v1.9.25 兜底：此前两类合法开箱会被误判"冷却中"——
		// ① 服务端重启后内存实例被清空，而客户端仍渲染着重启前的宝箱点位；
		// ② 打怪掉落的宝箱（nodeId 为 drop_ 前缀，由客户端物理落袋生成，不在配置表里）。
		// 仅当 nodeId 属于配置内宝箱点位或为掉落宝箱时按需创建（防伪造任意 ID 刷箱）。
		if !m.configHasChest(worldID, nodeID) && !strings.HasPrefix(nodeID, "drop_") {
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
	return *s, true
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
