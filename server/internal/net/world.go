// Package net · 世界/点位/宝箱权威接口（对应 /api/chest/open、/api/world/*、/api/spawn）。
// 所有数值由服务端按配置 roll，客户端只传意图（nodeId / worldId）。
package net

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"strings"
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

// chestDrop：怪物掉落宝箱登记（v1.9.45）。
// 客户端只上报"在哪个点位打败了什么怪"，档位由服务端按配置权威 roll；
// 返回登记后的 nodeId + tier，前端据此渲染对应贴图，开箱必然兑现同一档位。
// v1.9.46：请求体用 kind 区分 normal/elite/boss（旧客户端仍可只传 boss 布尔）。
func chestDrop(w http.ResponseWriter, r *http.Request) {
	var q struct {
		UserID  string `json:"userId"`
		WorldID string `json:"worldId"`
		NodeID  string `json:"nodeId"`
		Kind    string `json:"kind"`
		Boss    bool   `json:"boss"`
	}
	_ = json.NewDecoder(r.Body).Decode(&q)
	uid, ok := mustUser(w, r, q.UserID)
	if !ok {
		return
	}
	q.UserID = uid
	if q.WorldID == "" { q.WorldID = "W1" }
	if !strings.HasPrefix(q.NodeID, "drop_") || len(q.NodeID) > 64 {
		http.Error(w, "目标无效", 400)
		return
	}
	kind := q.Kind
	if kind == "" && q.Boss { kind = "boss" }
	kind = game.DropKindOf(kind)
	s, ok := game.SM().RegisterDropChest(q.UserID, q.WorldID, q.NodeID, kind)
	if !ok { http.Error(w, "掉落登记失败（nodeId 重复或未开启掉落箱过多）", 409); return }
	tierName := "wood"
	switch s.Tier {
	case 5: tierName = "crystal"
	case 4: tierName = "diamond"
	case 3: tierName = "gold"
	case 2: tierName = "silver"
	}
	jsonOut(w, map[string]any{"nodeId": s.NodeID, "tier": tierName, "kind": kind})
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
	uid, ok := mustUser(w, r, q.UserID)
	if !ok {
		return
	}
	if q.WorldID == "" { q.WorldID = "W1" }
	if q.NodeID == "" { http.Error(w, "nodeId 不能为空", 400); return }
	if q.Tier == "" { q.Tier = "wood" }

	p, err := loadOrCreate(uid)
	if err != nil { internalErr(w, err); return }

	// 点位状态机校验：宝箱必须处于 ACTIVE，并返回服务端权威档位
	spot, ok := game.SM().OpenChest(uid, q.WorldID, q.NodeID)
	if !ok {
		http.Error(w, "宝箱不可开启（冷却中或不存在）", 409); return
	}
	// 服务端权威 tier：忽略请求体 tier，按点位状态机 roll 出的档位发放（防客户端伪造）
	tierName := "wood"
	switch spot.Tier {
	case 5: tierName = "crystal" // v1.9.42 水晶箱
	case 4: tierName = "diamond" // v1.9.42 钻石箱
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
	// v1.9.46 地图产出系数：每张图比上一张多 20%（W1=1.0 / W2=1.2 / W3=1.44 … W6≈2.49）。
	// 金币/钻石/经验/材料/药水同步放大；装备仍按档位固定概率 roll（乘系数会把概率顶到 100%）。
	wm := game.WorldRewardMult(q.WorldID)
	if wm > 0 && wm != 1 {
		gold = int(float64(gold)*wm + 0.5)
		gem = int(float64(gem)*wm + 0.5)
	}
	// 经验/材料按 gold 派生（语义档位，外置比例，可改 economy 配置）
	exp := int(float64(gold) * numAny(game.C().Economy.Drop, "chestExpRatio", 1.5))
	mat := int(float64(gold) * numAny(game.C().Economy.Drop, "chestMatRatio", 0.1))
	// v1.9.35 详细掉落：药水按档位概率/数量 roll，装备按档位概率 roll 一件
	potions := 0
	switch q.Tier {
	case "crystal": // v1.9.42 水晶箱：必给 5~7 瓶
		potions = 5 + rand.Intn(3)
	case "diamond": // v1.9.42 钻石箱：必给 3~4 瓶
		potions = 3 + rand.Intn(2)
	case "gold":
		if rand.Intn(100) < 80 { potions = 2 + rand.Intn(2) }
	case "silver":
		if rand.Intn(100) < 60 { potions = 1 + rand.Intn(2) }
	default:
		if rand.Intn(100) < 40 { potions = 1 }
	}
	// v1.9.46 药水同享地图系数（四舍五入，木箱 1 瓶在高图会变 2 瓶）
	if wm > 1 && potions > 0 {
		potions = int(float64(potions)*wm + 0.5)
	}
	var equips []game.EquipItem
	if e, ok := game.RollChestEquip(p.Core.ClassID, q.Tier, rand.New(rand.NewSource(time.Now().UnixNano()))); ok {
		equips = []game.EquipItem{e}
	}
	logs, items := store.ApplyChestReward(p, exp, gold, gem, mat, potions, equips)
	if err := disk.Save(uid, p); err != nil { internalErr(w, err); return }
	jsonOut(w, map[string]any{
		"reward": map[string]any{"exp": exp, "gold": gold, "gem": gem, "tier": q.Tier, "items": items},
		"logs": logs, "state": p,
	})
}

// potionPick：v1.9.27 采集地图草药 → 服务端权威 roll 回血药水/金币入仓。
// 点位为玩家实例，采集后进入 potionBaseSec 冷却自动复活。
func potionPick(w http.ResponseWriter, r *http.Request) {
	var q struct {
		UserID  string `json:"userId"`
		WorldID string `json:"worldId"`
		NodeID  string `json:"nodeId"`
	}
	_ = json.NewDecoder(r.Body).Decode(&q)
	uid, ok := mustUser(w, r, q.UserID)
	if !ok {
		return
	}
	if q.WorldID == "" { q.WorldID = "W1" }
	if q.NodeID == "" { http.Error(w, "nodeId 不能为空", 400); return }

	p, err := loadOrCreate(uid)
	if err != nil { internalErr(w, err); return }
	if _, ok := game.SM().PickPotion(uid, q.WorldID, q.NodeID); !ok {
		http.Error(w, "草药尚未长成（冷却中或不存在）", 409); return
	}
	// 服务端权威 roll：1 瓶保底，30% 概率 +1；顺带少量金币
	potions := 1
	if rand.Intn(100) < 30 { potions = 2 }
	gold := 10 + rand.Intn(30)
	logs := store.ApplyPotionPick(p, potions, gold)
	if err := disk.Save(uid, p); err != nil { internalErr(w, err); return }
	jsonOut(w, map[string]any{
		"reward": map[string]any{"potions": potions, "gold": gold},
		"logs": logs, "state": p,
	})
}

// monsterKill：击败怪物 → 世界共享点位进入冷却（供战斗胜利后调用）。
func monsterKill(w http.ResponseWriter, r *http.Request) {
	if _, ok := mustUser(w, r, ""); !ok {
		return
	}
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
// 身份只认 token；query 里的 userId 若存在必须与 token 一致。
func spawnQuery(w http.ResponseWriter, r *http.Request) {
	uid, ok := mustUser(w, r, r.URL.Query().Get("userId"))
	if !ok {
		return
	}
	worldID := r.URL.Query().Get("worldId")
	if worldID == "" { worldID = "W1" }
	spots := game.SM().ActiveSpots(worldID, uid)
	jsonOut(w, map[string]any{"worldId": worldID, "spots": spots, "serverNow": game.ServerNow()})
}

// worldUnlocks：返回玩家的世界解锁状态 + 可传送 GATE 列表。
func worldUnlocks(w http.ResponseWriter, r *http.Request) {
	uid, ok := mustUser(w, r, r.URL.Query().Get("userId"))
	if !ok {
		return
	}
	p, err := loadOrCreate(uid)
	if err != nil { internalErr(w, err); return }
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
	var q struct {
		To     string `json:"to"`
		UserID string `json:"userId"`
	}
	_ = json.NewDecoder(r.Body).Decode(&q)
	uid, ok := mustUser(w, r, q.UserID)
	if !ok {
		return
	}
	p, err := loadOrCreate(uid)
	if err != nil { internalErr(w, err); return }
	// 目标世界必须存在
	var target *game.WorldDef
	for i := range game.C().Worlds.Worlds {
		if game.C().Worlds.Worlds[i].ID == q.To { target = &game.C().Worlds.Worlds[i] }
	}
	if target == nil { http.Error(w, "目标世界不存在", 400); return }
	// v1.9.34：HOME 幂等——战败/API 已把存档切到 HOME 但前端地图未同步时，
	// 再点家园按钮不应报"已在当前世界"卡死，直接返回成功让前端对齐。
	if q.To == "HOME" {
		if p.World.WorldID != "HOME" {
			p.World.LastWorldID = p.World.WorldID
		}
		p.World.WorldID = "HOME"
		p.World.X, p.World.Y = 1280, 780 // 家园锚点
		if err := disk.Save(uid, p); err != nil { internalErr(w, err); return }
		jsonOut(w, map[string]any{"worldId": "HOME", "state": p})
		return
	}
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
	// v1.9.24：传送 CD 已取消（与宝箱冷却同期需求）
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
	if err := disk.Save(uid, p); err != nil { internalErr(w, err); return }
	jsonOut(w, map[string]any{"worldId": q.To, "state": p})
}

func containsStr(ss []string, s string) bool { for _, x := range ss { if x == s { return true } }; return false }

// ---- 公会（v1.9.18）：服务端权威的创建 / 加入 / 退出 / 列表 ----

// guildView 公会对客户端的展示结构（等级实时按进度折算）。
func guildView(g *store.Guild) map[string]any {
	ms := make([]map[string]any, 0, len(g.Members))
	for _, m := range g.Members {
		// v1.9.61 战力/等级改为实时读取：原来只存"入会/捐献那一刻"的快照，
		// 成员升级换装后公会面板一直显示旧战力（没捐过献的甚至一直是入会时的值）。
		level, power := m.Level, m.Power
		if p, err := disk.Load(m.UserID); err == nil && p != nil {
			level = p.Core.Level
			power = livePower(p)
		}
		ms = append(ms, map[string]any{
			"userId": m.UserID, "name": m.Name, "level": level,
			"power": power, "role": m.Role, "donate": m.Donate, "joinedAt": m.JoinedAt,
		})
	}
	return map[string]any{
		"id": g.ID, "name": g.Name, "level": g.Level(), "progress": g.Progress,
		"members": ms, "size": len(g.Members), "createdAt": g.CreatedAt,
	}
}

// guildList（v1.9.39 公会界面大改）：返回公会列表 + 当前玩家的所在公会 my（服务端权威）。
// 前端按 my 分流：有 my → 我的公会主界面；无 my → 搜索/加入界面。
func guildList(w http.ResponseWriter, r *http.Request) {
	var q struct{ UserID string }
	_ = json.NewDecoder(r.Body).Decode(&q)
	uid, ok := mustUser(w, r, q.UserID)
	if !ok {
		return
	}
	out := []map[string]any{}
	for _, g := range store.Guilds().GuildList() {
		out = append(out, guildView(g))
	}
	resp := map[string]any{"guilds": out}
	if g, role := store.Guilds().GuildByUser(uid); g != nil {
		resp["my"] = guildView(g)
		resp["myRole"] = role
	}
	jsonOut(w, resp)
}

// guildActionBody 解析公会操作请求体：身份只认 token，body userId 必须一致。
func guildActionBody(w http.ResponseWriter, r *http.Request) (string, string, string, bool) {
	var q struct{ UserID, Name, GuildID string }
	_ = json.NewDecoder(r.Body).Decode(&q)
	uid, ok := mustUser(w, r, q.UserID)
	if !ok {
		return "", "", "", false
	}
	return uid, q.Name, q.GuildID, true
}

func guildCreate(w http.ResponseWriter, r *http.Request) {
	uid, name, _, ok := guildActionBody(w, r)
	if !ok {
		return
	}
	p, err := loadOrCreate(uid)
	if err != nil { internalErr(w, err); return }
	if p.Guild != nil { http.Error(w, "你已加入公会，请先退出再创建", 409); return }
	if p.Core.Gold < store.GuildCreateCost {
		http.Error(w, fmt.Sprintf("金币不足：创建公会需 %d（当前 %d）", store.GuildCreateCost, p.Core.Gold), 400); return
	}
	gid := fmt.Sprintf("g_%s_%d", uid[:8], time.Now().Unix()%100000)
	g, err := store.Guilds().GuildCreate(gid, name, p.UserID, p.Core.Name, p.Core.Level, p.Core.BattlePower)
	if err != nil { http.Error(w, err.Error(), 409); return }
	p.Core.Gold -= store.GuildCreateCost
	p.Guild = &store.GuildRef{ID: g.ID, Name: g.Name, Role: "president"}
	p.AppendLogs([]string{fmt.Sprintf("创建公会：%s（消耗 %d 金币）", g.Name, store.GuildCreateCost)})
	if err := disk.Save(uid, p); err != nil { internalErr(w, err); return }
	jsonOut(w, map[string]any{"state": p, "guild": guildView(g), "message": "公会创建成功"})
}

func guildJoin(w http.ResponseWriter, r *http.Request) {
	uid, _, gid, ok := guildActionBody(w, r)
	if !ok {
		return
	}
	p, err := loadOrCreate(uid)
	if err != nil { internalErr(w, err); return }
	if p.Guild != nil { http.Error(w, "你已加入公会，请先退出再加入其他公会", 409); return }
	g, err := store.Guilds().GuildJoin(gid, p.UserID, p.Core.Name, p.Core.Level, p.Core.BattlePower)
	if err != nil { http.Error(w, err.Error(), 409); return }
	p.Guild = &store.GuildRef{ID: g.ID, Name: g.Name, Role: "member"}
	p.AppendLogs([]string{fmt.Sprintf("加入公会：%s", g.Name)})
	if err := disk.Save(uid, p); err != nil { internalErr(w, err); return }
	jsonOut(w, map[string]any{"state": p, "guild": guildView(g), "message": "加入公会成功"})
}

func guildLeave(w http.ResponseWriter, r *http.Request) {
	uid, _, _, ok := guildActionBody(w, r)
	if !ok {
		return
	}
	p, err := loadOrCreate(uid)
	if err != nil { internalErr(w, err); return }
	if p.Guild == nil { http.Error(w, "你尚未加入公会", 409); return }
	gname := p.Guild.Name
	rest, err := store.Guilds().GuildLeave(p.Guild.ID, p.UserID)
	if err != nil { http.Error(w, err.Error(), 409); return }
	p.Guild = nil
	if rest == nil {
		p.AppendLogs([]string{fmt.Sprintf("公会 %s 已解散（最后一名成员退出）", gname)})
	} else {
		p.AppendLogs([]string{fmt.Sprintf("退出公会：%s", gname)})
	}
	if err := disk.Save(uid, p); err != nil { internalErr(w, err); return }
	jsonOut(w, map[string]any{"state": p, "message": "已退出公会"})
}
