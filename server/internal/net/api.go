// Package net 提供 HTTP API（standalone 初跑模式），带磁盘存档持久化。
// 生产模式用 Nakama RPC 调用同样的 game/store 包逻辑。
package net

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
	"os"

	"zjcs/server/internal/game"
	"zjcs/server/internal/store"
)

var disk *store.DiskStore
var webFile string // 可选：前端单文件页面路径，非空时由 "/" 托管
var webResDir string // 可选：静态资源目录（png 等），由 /res/ 托管

// Handler 聚合所有 API 路由（兼容旧调用，不托管前端）。
func Handler(cfgDir string) http.Handler {
	return HandlerWithWeb(cfgDir, "", "")
}

// HandlerWithWeb 聚合所有 API 路由，并可托管前端页面与静态资源（同源部署，单端口）。
func HandlerWithWeb(cfgDir, webFileArg, resDir string) http.Handler {
	webFile = webFileArg
	webResDir = resDir
	if err := game.Init(cfgDir); err != nil {
		panic("配置加载失败: " + err.Error())
	}
	var err error
	dataDir := cfgDir + "/../data"
	disk, err = store.NewDiskStore(dataDir)
	if err != nil {
		panic("存档初始化失败: " + err.Error())
	}
	accounts, err = NewAccountStore(dataDir)
	if err != nil {
		panic("账号库初始化失败: " + err.Error())
	}
	initTokenSecret(dataDir)
	game.InitSpawns()
	mux := http.NewServeMux()
	mux.HandleFunc("/health", health)
	mux.HandleFunc("/api/config", getConfig)
	mux.HandleFunc("/api/tables", tables)

	mux.HandleFunc("/api/character/new", characterNew)
	mux.HandleFunc("/api/state/load", stateLoad)

	// 账号体系（Nakama 语义同构）
	mux.HandleFunc("/api/auth/device", deviceLogin)
	mux.HandleFunc("/api/auth/register", registerAccount)
	mux.HandleFunc("/api/auth/login", loginAccount)
	mux.HandleFunc("/api/auth/bind-device", bindDevice)

	// 世界/点位/宝箱权威接口
	mux.HandleFunc("/api/chest/open", chestOpen)
	mux.HandleFunc("/api/monster/kill", monsterKill)
	mux.HandleFunc("/api/spawn", spawnQuery)
	mux.HandleFunc("/api/world/unlocks", worldUnlocks)
	mux.HandleFunc("/api/world/travel", worldTravel)

	// 实时多人（同端口 WebSocket）
	mux.HandleFunc("/ws", wsHandler)

	mux.HandleFunc("/api/stats/calc", calcStats)
	mux.HandleFunc("/api/battle/resolve", resolveBattle) // 纯结算（不落库，便于回放）
	mux.HandleFunc("/api/battle/fight", battleFight)     // 战斗 + 落库
	mux.HandleFunc("/api/idle/settle", idleSettle)       // 纯结算
	mux.HandleFunc("/api/idle/finalize", idleFinalize)   // 离线 + 落库
	mux.HandleFunc("/api/transfer", transferClass)       // 转职 + 落库
	mux.HandleFunc("/api/player/action", playerAction)   // BD/装备/仓库/副本/家园 权威操作 + 落库

	if webResDir != "" {
		// 静态资源统一协商缓存：no-cache = 每次向服务端校验（未变更返回 304，几乎零开销），
		// 避免浏览器启发式缓存把旧版 JS 缓存住导致"更新不生效/依旧卡顿"。
		fs := http.StripPrefix("/res/", http.FileServer(http.Dir(webResDir)))
		mux.Handle("/res/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-cache")
			fs.ServeHTTP(w, r)
		}))
	}
	mux.HandleFunc("/", index)
	return cors(mux)
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func jsonOut(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func health(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, map[string]any{"ok": true, "service": "zjcs-authoritative-server", "mode": "standalone", "persist": "disk"})
}

func getConfig(w http.ResponseWriter, r *http.Request) {
	c := game.C()
	jsonOut(w, map[string]any{
		"classes": c.Classes, "skills": c.Skills, "equipment": c.Equipment,
		"monsters": c.Monsters, "economy": c.Economy, "worlds": c.Worlds,
	})
}

func tables(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, map[string]any{"tables": store.TableNames, "persist": "data/saves/<userId>.json"})
}

// ---------- 存档 ----------

func characterNew(w http.ResponseWriter, r *http.Request) {
	var q struct{ UserID, Name, ClassID string }
	_ = json.NewDecoder(r.Body).Decode(&q)
	if q.UserID == "" {
		q.UserID = "anonymous"
	}
	p := store.NewPlayer(q.UserID, q.Name, q.ClassID)
	if err := disk.Save(q.UserID, p); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	jsonOut(w, p)
}

func stateLoad(w http.ResponseWriter, r *http.Request) {
	var q struct{ UserID string }
	_ = json.NewDecoder(r.Body).Decode(&q)
	if q.UserID == "" {
		q.UserID = "anonymous"
	}
	p, err := disk.Load(q.UserID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if p == nil {
		jsonOut(w, map[string]any{"exists": false})
		return
	}
	// 老存档迁移（v1.8）：世界分辨率由 15360×8640 折半为 7680×4320，
	// 坐标超出新世界范围的存档按比例折半；折半后若落在旧默认出生点（中央湖内）
	// 或边界外，统一点落回 W1 通道安全点。
	migrated := false
	if p.World.X > 7680 || p.World.Y > 4320 {
		p.World.X, p.World.Y = p.World.X/2, p.World.Y/2
		migrated = true
	}
	if p.World.WorldID == "W1" && p.World.X >= 3700 && p.World.X <= 4000 && p.World.Y >= 2050 && p.World.Y <= 2260 {
		p.World.X, p.World.Y = 2560, 2176
		migrated = true
	}
	if p.World.X < 32 || p.World.Y < 32 || p.World.X > 7648 || p.World.Y > 4288 {
		p.World.X, p.World.Y = 2560, 2176
		migrated = true
	}
	if migrated {
		_ = disk.Save(q.UserID, p)
	}
	jsonOut(w, map[string]any{"exists": true, "state": p})
}

// ---------- 纯结算（不落库，回放/验证用）----------

func calcStats(w http.ResponseWriter, r *http.Request) {
	var a game.Avatar
	if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	jsonOut(w, game.CalcStats(a))
}

func resolveBattle(w http.ResponseWriter, r *http.Request) {
	var req game.BattleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	jsonOut(w, game.ResolveBattle(req))
}

func idleSettle(w http.ResponseWriter, r *http.Request) {
	var in game.IdleInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if in.Now == 0 {
		in.Now = game.ServerNow()
	}
	jsonOut(w, game.SettleIdle(in))
}

// ---------- 结算 + 落库（真实游戏链路）----------

func battleFight(w http.ResponseWriter, r *http.Request) {
	var q struct {
		UserID    string   `json:"userId"`
		BD        []string `json:"bd"`
		MonsterID string   `json:"monsterId"`
		MonsterLv int      `json:"monsterLv"`
		Elite     string   `json:"elite"`
	}
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if q.UserID == "" {
		q.UserID = "anonymous"
	}
	p, err := loadOrCreate(q.UserID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	equip := make([]game.EquipItem, 0, len(p.Equip))
	for _, e := range p.Equip {
		if e.Equipped {
			equip = append(equip, game.EquipItem{ID: e.ID, Slot: e.Slot, Quality: e.Quality, Enhance: e.Enhance, Stats: e.Stats})
		}
	}
	// 武器解封：等级不足的技能自动降级为普攻（保守估算，服务端裁定）
	bd := filterBD(q.BD, p.Core.Level)
	rep := game.ResolveBattle(game.BattleRequest{
		UserID: q.UserID, Avatar: game.Avatar{Level: p.Core.Level, ClassID: p.Core.ClassID, Equip: equip},
		BD: bd, MonsterID: q.MonsterID, MonsterLv: q.MonsterLv, Elite: q.Elite,
	})
	logs := store.ApplyBattleReport(p, rep)
	if err := disk.Save(q.UserID, p); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	jsonOut(w, map[string]any{"report": rep, "logs": logs, "state": p})
}

func idleFinalize(w http.ResponseWriter, r *http.Request) {
	var q struct{ UserID string }
	_ = json.NewDecoder(r.Body).Decode(&q)
	if q.UserID == "" {
		q.UserID = "anonymous"
	}
	p, err := loadOrCreate(q.UserID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	now := game.ServerNow()
	res := game.SettleIdle(game.IdleInput{Now: now, LastSeen: p.World.LastSeen, Level: p.Core.Level})
	store.ApplyIdleResult(p, res, now)
	if err := disk.Save(q.UserID, p); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	jsonOut(w, map[string]any{"result": res, "state": p})
}

func transferClass(w http.ResponseWriter, r *http.Request) {
	var q struct{ UserID, To string }
	_ = json.NewDecoder(r.Body).Decode(&q)
	if q.UserID == "" {
		q.UserID = "anonymous"
	}
	p, err := loadOrCreate(q.UserID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	old := make([]game.EquipItem, 0, len(p.Equip))
	for _, e := range p.Equip {
		old = append(old, game.EquipItem{ID: e.ID, Slot: e.Slot, Quality: e.Quality, Enhance: e.Enhance, Stats: e.Stats})
	}
	converted := game.TransferEquip(old, q.To)
	for i := range p.Equip {
		p.Equip[i].Stats = converted[i].Stats
	}
	p.Core.ClassID = q.To
	if err := disk.Save(q.UserID, p); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	jsonOut(w, map[string]any{"classId": q.To, "message": "转职成功，装备属性已按新职业系数换算", "state": p})
}

// playerAction：玩家操作的单一权威入口（BD/装备/仓库/副本/家园）。
// 客户端只传意图与参数，服务端校验后落库并返回最新 state。
func playerAction(w http.ResponseWriter, r *http.Request) {
	var q struct {
		UserID  string   `json:"userId"`
		Action  string   `json:"action"`
		Slots   []string `json:"slots"`   // submit_bd
		SkillID string   `json:"skillId"` // unlock_skill
		ItemID  string   `json:"itemId"`  // equip/unequip/deposit/withdraw
		Qty     int      `json:"qty"`     // deposit/withdraw
		Slot    string   `json:"slot"`    // enhance
		DGID    string   `json:"dungeonId"`
		QuestID string   `json:"questId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	uid, ok := authUserID(r)
	if !ok && q.UserID != "" { uid = q.UserID }
	if uid == "" { uid = "anonymous" }
	p, err := loadOrCreate(uid)
	if err != nil { http.Error(w, err.Error(), 500); return }

	var logs []string
	var extra map[string]any = map[string]any{}
	switch q.Action {
	case "submit_bd":
		slots, e := p.SubmitBD(q.Slots)
		if e != nil { http.Error(w, e.Error(), 400); return }
		extra["slots"] = slots
	case "unlock_skill":
		cost := 300
		unlocked, e := p.UnlockSkill(q.SkillID, cost)
		if e != nil { http.Error(w, e.Error(), 400); return }
		extra["unlocked"] = unlocked
	case "equip":
		logs, err = p.EquipItem(q.ItemID)
		if err != nil { http.Error(w, err.Error(), 400); return }
	case "unequip":
		logs, err = p.UnequipItem(q.ItemID)
		if err != nil { http.Error(w, err.Error(), 400); return }
	case "enhance":
		goldCost := 1200
		matCost := 2
		if enh, ok := game.C().Equipment.Enhance["costBase"].(float64); ok {
			goldCost = int(enh)
		}
		lvl, e := p.EnhanceEquip(q.Slot, goldCost, matCost)
		if e != nil { http.Error(w, e.Error(), 400); return }
		extra["enhanceLevel"] = lvl
	case "deposit":
		if err = p.WarehouseDeposit(q.ItemID, q.Qty); err != nil { http.Error(w, err.Error(), 400); return }
	case "withdraw":
		if err = p.WarehouseWithdraw(q.ItemID, q.Qty); err != nil { http.Error(w, err.Error(), 400); return }
	case "dungeon_enter":
		perDay := 0
		var mon string; var elite bool; var lvReq int
		for _, d := range game.C().Economy.Dungeons {
			if d.ID == q.DGID { perDay = d.PerDay; mon = d.Mon; elite = d.Elite; lvReq = d.Level; break }
		}
		if perDay <= 0 { http.Error(w, "副本不存在", 400); return }
		if p.Core.Level < lvReq { http.Error(w, fmt.Sprintf("等级不足：需 Lv.%d", lvReq), 403); return }
		remain, e := p.DungeonEnter(q.DGID, perDay)
		if e != nil { http.Error(w, e.Error(), 409); return }
		extra["remain"] = remain
		extra["monsterId"] = mon
		extra["elite"] = elite
	case "dungeon_remain":
		perDay := 0
		for _, d := range game.C().Economy.Dungeons {
			if d.ID == q.DGID { perDay = d.PerDay; break }
		}
		extra["remain"] = p.DungeonRemain(q.DGID, perDay)
	case "guild_donate":
		if p.Ext == nil { p.Ext = map[string]any{} }
		today := time.Now().Format("2006-01-02")
		if d, _ := p.Ext["guildDay"].(string); d != today {
			p.Ext["guildDay"] = today
			p.Ext["guildDonated"] = float64(0)
		}
		donated := 0
		if v, ok := p.Ext["guildDonated"].(float64); ok { donated = int(v) }
		if donated >= 3 { http.Error(w, "今日捐献次数已用完（3/3）", 409); return }
		const cost = 1000
		if p.Core.Gold < cost { http.Error(w, "金币不足（需 1,000）", 400); return }
		p.Core.Gold -= cost
		coin := 0
		if v, ok := p.Ext["guildCoin"].(float64); ok { coin = int(v) }
		coin += 50
		p.Ext["guildCoin"] = float64(coin)
		p.Ext["guildDonated"] = float64(donated + 1)
		p.GainExp(60)
		extra["guildCoin"] = coin
		extra["guildDonated"] = donated + 1
	case "guild_buy":
		if p.Ext == nil { p.Ext = map[string]any{} }
		prices := map[string]int{"gem_pack": 200, "material_pack": 150, "skill_book": 300}
		price, ok := prices[q.ItemID]
		if !ok { http.Error(w, "未知商品", 400); return }
		coin := 0
		if v, ok := p.Ext["guildCoin"].(float64); ok { coin = int(v) }
		if coin < price { http.Error(w, "公会币不足", 400); return }
		coin -= price
		p.Ext["guildCoin"] = float64(coin)
		switch q.ItemID {
		case "gem_pack": p.Core.Gem += 60
		case "material_pack": p.Core.Material += 30
		case "skill_book": p.Core.Gem += 30; p.GainExp(300)
		}
		extra["guildCoin"] = coin
	case "quest_claim":
		rw, ok := questRewards[q.QuestID]
		if !ok { http.Error(w, "未知任务", 400); return }
		if p.Ext == nil { p.Ext = map[string]any{} }
		claimed, _ := p.Ext["quests"].(map[string]any)
		if claimed == nil { claimed = map[string]any{} }
		if claimed[q.QuestID] == true { http.Error(w, "该任务奖励已领取", 409); return }
		claimed[q.QuestID] = true
		p.Ext["quests"] = claimed
		p.Core.Gold += rw.Gold
		p.Core.Gem += rw.Gem
		p.Core.Material += rw.Mat
		p.GainExp(rw.Exp)
		extra["reward"] = map[string]int{"gold": rw.Gold, "gem": rw.Gem, "exp": rw.Exp, "material": rw.Mat}
	case "home_decorate":
		// 家园装饰（占位升级：记录到 Home，服务端权威）
		item := q.ItemID
		switch item {
		case "cart": p.Home.Cart++
		case "bed": p.Home.Bed++
		default: http.Error(w, "未知装饰", 400); return
		}
		p.Logs = append(p.Logs, store.LogEntry{Event: fmt.Sprintf("家园装饰升级：%s", item)})
	default:
		http.Error(w, "未知操作："+q.Action, 400)
		return
	}
	if err := disk.Save(uid, p); err != nil { http.Error(w, err.Error(), 500); return }
	extra["logs"] = logs
	extra["state"] = p
	jsonOut(w, extra)
}

// 主线任务奖励（服务端白名单：前端只报 questId，金额不可篡改）
type questReward struct{ Gold, Gem, Exp, Mat int }
var questRewards = map[string]questReward{
	"q_kill3":    {800, 30, 300, 0},
	"q_chest1":   {600, 20, 200, 2},
	"q_equip4":   {900, 40, 400, 0},
	"q_enhance1": {700, 25, 250, 1},
	"q_travel":   {1500, 80, 800, 5},
}

// ---------- 辅助 ----------

func loadOrCreate(userID string) (*store.PlayerState, error) {
	p, err := disk.Load(userID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		p = store.NewPlayer(userID, "", "")
		if err := disk.Save(userID, p); err != nil {
			return nil, err
		}
	}
	return p, nil
}

// filterBD 初跑保守处理：预留等级门槛扩展点，当前原样返回（技能解锁由客户端按配置限制）。
func filterBD(bd []string, level int) []string {
	return bd
}

func index(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-cache") // HTML 每次校验，确保引用到最新脚本
	if webFile != "" {
		if b, err := os.ReadFile(webFile); err == nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(b)
			return
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(`<!doctype html><meta charset=utf-8><title>ZJCS Server</title>
<h2>杖剑传说 · 权威服务端</h2><p>standalone 模式运行中，存档持久化到 data/saves/。</p>
<ul><li>/health 健康检查</li><li>/api/config 配置</li><li>/api/tables 分表清单</li>
<li>/api/character/new 创角</li><li>/api/state/load 读档</li>
<li>/api/stats/calc 属性计算</li><li>/api/battle/resolve 纯战斗结算</li><li>/api/battle/fight 战斗+落库</li>
<li>/api/idle/settle 纯离线结算</li><li>/api/idle/finalize 离线+落库</li><li>/api/transfer 转职+落库</li></ul>`))
}
