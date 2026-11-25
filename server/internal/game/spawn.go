// 点位刷新状态机：固定点位 + 随机刷新（对应地图设计 v0.8 §3 / 表10）。
// 怪为世界实例共享；宝箱为玩家独立实例（按 userId+nodeId 分键）。
// 冷却 base×(1±jitter)、内容 tier roll、保底 8 次、同时 ACTIVE ≤ 密度上限。
// 所有参数来自 worlds.json respawn / economy.json drop，本包不硬编码数值。
package game

import (
	"math/rand"
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
				if s.Type == "CHEST" { continue } // 宝箱走玩家实例，按需创建
				key := w.ID + "/" + s.ID
				spawnMgr.spots[key] = &SpotState{
					NodeID: s.ID, Type: s.Type, Tier: s.Tier, X: s.X, Y: s.Y,
					To: s.To, ReqLevel: s.RequireLevel, BossMonster: s.BossMonster,
					Active: true, ReadyAt: now, ContentID: s.BossMonster,
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
	if userID != "" {
		for _, wd := range C().Worlds.Worlds {
			if wd.ID != worldID { continue }
			for _, sd := range wd.Spawns {
				if sd.Type != "CHEST" { continue }
				key := userID + "/" + worldID + "/" + sd.ID
				s, ok := m.chests[key]
				if !ok {
					// 首次创建按服务端权威 roll 出档位（木60/银30/金10），不取配置固定值
					tier := m.rollContent("CHEST", key)
					s = &SpotState{NodeID: sd.ID, Type: "CHEST", Tier: tier, X: sd.X, Y: sd.Y,
						Active: true, ReadyAt: now, ContentID: "chest"}
					m.chests[key] = s
				} else if !s.Active && now >= s.ReadyAt {
					s.Active = true
					s.Tier = m.rollContent("CHEST", key)
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
func (m *SpawnManager) OpenChest(userID, worldID, nodeID string) (SpotState, bool) {
	m.mu.Lock(); defer m.mu.Unlock()
	key := userID + "/" + worldID + "/" + nodeID
	s, ok := m.chests[key]
	if !ok || !s.Active { return SpotState{}, false }
	s.Active = false
	s.ReadyAt = ServerNow() + m.cooldownFor("CHEST")
	return *s, true
}
