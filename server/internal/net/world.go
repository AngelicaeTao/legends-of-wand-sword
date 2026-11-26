// Package net · 世界/点位/宝箱权威接口（对应 /api/chest/open、/api/world/*、/api/spawn）。
// 所有数值由服务端按配置 roll，客户端只传意图（nodeId / worldId）。
package net

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"time"

	"zjcs/server/internal/game"
	"zjcs/server/internal/store"
)

// numAny 从 map[string]any 安全取数
func numAny(m map[string]any, k string, def float64) float64 {
	if v, ok := m[k]; ok {
		switch n := v.(type) {
		case float64: return n
		case int: return float64(n)
		case int64: return float64(n)
		}
	}
	return def
}

// chestOpen：服务端按 tier 权威 roll 奖励并入仓库/存档。
func chestOpen(w http.ResponseWriter, r *http.Request) {
	var q struct {
		UserID  string `json:"userId"`
		WorldID string `json:"worldId"`
		NodeID  string `json:"nodeId"`
		Tier    string `json:"tier"`
	}
	_ = json.NewDecoder(r.Body).Decode(&q)
	uid, ok := authUserID(r)
	if !ok && q.UserID != "" { uid = q.UserID } // 兼容旧链路
	if uid == "" { http.Error(w, "未授权", 401); return }
	if q.WorldID == "" { q.WorldID = "W1" }
	if q.NodeID == "" { http.Error(w, "nodeId 不能为空", 400); return }
	if q.Tier == "" { q.Tier = "wood" }

	p, err := loadOrCreate(uid)
	if err != nil { http.Error(w, err.Error(), 500); return }

	// 点位状态机校验：宝箱必须处于 ACTIVE，并返回服务端权威档位
	spot, ok := game.SM().OpenChest(uid, q.WorldID, q.NodeID)
	if !ok {
		http.Error(w, "宝箱不可开启（冷却中或不存在）", 409); return
	}
	// 服务端权威 tier：忽略请求体 tier，按点位状态机 roll 出的档位发放（防客户端伪造）
	tierName := "wood"
	switch spot.Tier {
	case 3: tierName = "gold"
	case 2: tierName = "silver"
	default: tierName = "wood"
	}
	q.Tier = tierName

	// 按 tier 从 economy.drop.chest 权威 roll（语义档位：wood/silver/gold）
	drop := game.C().Economy.Drop["chest"]
	tierMap, _ := drop.(map[string]any)
	cfg, _ := tierMap[q.Tier].(map[string]any)
	if cfg == nil { cfg = tierMap["wood"].(map[string]any) }
	rg := func(key string) int {
		arr, _ := cfg[key].([]any)
		if len(arr) >= 2 {
			lo, hi := numAny(map[string]any{"v": arr[0]}, "v", 0), numAny(map[string]any{"v": arr[1]}, "v", 0)
			return int(lo) + rand.Intn(int(hi-lo)+1)
		}
		return 0
	}
	gold, gem := rg("gold"), rg("gem")
	// 经验/材料按 gold 派生（语义档位，外置比例，可改 economy 配置）
	exp := int(float64(gold) * numAny(game.C().Economy.Drop, "chestExpRatio", 1.5))
	mat := int(float64(gold) * numAny(game.C().Economy.Drop, "chestMatRatio", 0.1))
	logs := store.ApplyChestReward(p, exp, gold, gem, mat)
	if err := disk.Save(uid, p); err != nil { http.Error(w, err.Error(), 500); return }
	jsonOut(w, map[string]any{
		"reward": map[string]any{"exp": exp, "gold": gold, "gem": gem, "material": mat, "tier": q.Tier},
		"logs": logs, "state": p,
	})
}

// monsterKill：击败怪物 → 世界共享点位进入冷却（供战斗胜利后调用）。
func monsterKill(w http.ResponseWriter, r *http.Request) {
	var q struct {
		WorldID string `json:"worldId"`
		NodeID  string `json:"nodeId"`
	}
	_ = json.NewDecoder(r.Body).Decode(&q)
	if q.WorldID == "" || q.NodeID == "" { http.Error(w, "worldId/nodeId 不能为空", 400); return }
	ok := game.SM().KillMonster(q.WorldID, q.NodeID)
	jsonOut(w, map[string]any{"ok": ok})
}

// spawnQuery：查询世界当前 ACTIVE 点位（含玩家宝箱实例）。
// userId 优先取 query 参数（前端传 uid()，避免网关剥离 token 导致宝箱丢失），再 fallback token。
func spawnQuery(w http.ResponseWriter, r *http.Request) {
	uid := r.URL.Query().Get("userId")
	if uid == "" { uid, _ = authUserID(r) }
	worldID := r.URL.Query().Get("worldId")
	if worldID == "" { worldID = "W1" }
	spots := game.SM().ActiveSpots(worldID, uid)
	jsonOut(w, map[string]any{"worldId": worldID, "spots": spots, "serverNow": game.ServerNow()})
}

// reqUserID 统一取 userId：query/body 优先（前端传 uid()），再 fallback token。
func reqUserID(r *http.Request) string {
	if u := r.URL.Query().Get("userId"); u != "" { return u }
	if u, ok := authUserID(r); ok { return u }
	return ""
}

// worldUnlocks：返回玩家的世界解锁状态 + 可传送 GATE 列表。
func worldUnlocks(w http.ResponseWriter, r *http.Request) {
	uid := reqUserID(r)
	if uid == "" { http.Error(w, "未授权", 401); return }
	p, err := loadOrCreate(uid)
	if err != nil { http.Error(w, err.Error(), 500); return }
	gates := []map[string]any{}
	for _, wd := range game.C().Worlds.Worlds {
		for _, s := range wd.Spawns {
			if s.Type == "GATE" {
				gates = append(gates, map[string]any{
					"from": wd.ID, "to": s.To, "requireLevel": s.RequireLevel,
					"nodeId": s.ID, "x": s.X, "y": s.Y,
				})
			}
		}
	}
	jsonOut(w, map[string]any{"unlocked": p.World.Unlocked, "current": p.World.WorldID, "gates": gates})
}

// worldTravel：世界切换/传送（免费 5s CD，服务端权威），校验等级门槛。
func worldTravel(w http.ResponseWriter, r *http.Request) {
	uid := reqUserID(r)
	var q struct {
		To     string `json:"to"`
		UserID string `json:"userId"`
	}
	_ = json.NewDecoder(r.Body).Decode(&q)
	if uid == "" { uid = q.UserID }
	if uid == "" { http.Error(w, "未授权", 401); return }
	p, err := loadOrCreate(uid)
	if err != nil { http.Error(w, err.Error(), 500); return }
	// 目标世界必须存在
	var target *game.WorldDef
	for i := range game.C().Worlds.Worlds {
		if game.C().Worlds.Worlds[i].ID == q.To { target = &game.C().Worlds.Worlds[i] }
	}
	if target == nil { http.Error(w, "目标世界不存在", 400); return }
	if q.To == p.World.WorldID { http.Error(w, "已在当前世界", 400); return }
	// 找当前世界通往目标的 GATE：有通道 → 校验等级门槛；
	// 没有通道但目标已解锁（回访已探索世界）→ 允许直接传送。
	// 旧逻辑只认 GATE，导致"从迷雾森林回青草平原"被判成"无通道"，与面板上
	// "点已解锁世界可直接传送"的说明自相矛盾。
	var gate *game.SpawnDef
	for _, wd := range game.C().Worlds.Worlds {
		if wd.ID != p.World.WorldID { continue }
		for i, s := range wd.Spawns {
			if s.Type == "GATE" && s.To == q.To { gate = &wd.Spawns[i] }
		}
	}
	if gate == nil {
		if !containsStr(p.World.Unlocked, q.To) {
			http.Error(w, "尚未解锁该世界：请沿世界通道推进", 400)
			return
		}
	} else if p.Core.Level < gate.RequireLevel {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(403)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": fmt.Sprintf("等级不足：当前 Lv.%d，前往 %s 需 Lv.%d，请先打怪或挂机升级", p.Core.Level, target.Name, gate.RequireLevel)})
		return
	}
	// 传送 CD：服务端记录上次传送时间（放 Ext）
	last, _ := p.Ext["lastTravel"].(float64)
	now := float64(time.Now().Unix())
	if now-last < 5 { http.Error(w, "传送冷却中（5s）", 429); return }
	p.Ext["lastTravel"] = now
	p.World.WorldID = q.To
	if !containsStr(p.World.Unlocked, q.To) { p.World.Unlocked = append(p.World.Unlocked, q.To) }
	// 落到新世界锚点
	for _, wd := range game.C().Worlds.Worlds {
		if wd.ID == q.To {
			for _, s := range wd.Spawns {
				if s.Type == "ANCHOR" { p.World.X, p.World.Y = s.X, s.Y }
			}
		}
	}
	if err := disk.Save(uid, p); err != nil { http.Error(w, err.Error(), 500); return }
	jsonOut(w, map[string]any{"worldId": q.To, "state": p})
}

func containsStr(ss []string, s string) bool { for _, x := range ss { if x == s { return true } }; return false }
