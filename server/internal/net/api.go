// Package net 提供 HTTP API（standalone 初跑模式），带磁盘存档持久化。
// 生产模式用 Nakama RPC 调用同样的 game/store 包逻辑。
package net

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
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
// FlushSaves 优雅关闭前刷盘（v1.9.41 异步落盘配套）。
func FlushSaves() error {
	if disk == nil { return nil }
	return disk.FlushAll()
}

// decomposeTables 从 economy.drop.decompose 读品质基准表（单件分解与升品共用）。
func decomposeTables() (goldBase, matBase []int) {
	gb := []int{40, 120, 300, 800, 2000, 4200, 8000, 15000}
	mb := []int{10, 30, 80, 200, 500, 1200, 2600, 6000}
	if cfg, ok := game.C().Economy.Drop["decompose"].(map[string]any); ok {
		if b, ok := cfg["goldBase"].([]any); ok && len(b) > 0 {
			gb = make([]int, len(b))
			for i, v := range b { gb[i] = int(numAny(map[string]any{"v": v}, "v", 0)) }
		}
		if b, ok := cfg["matBase"].([]any); ok && len(b) > 0 {
			mb = make([]int, len(b))
			for i, v := range b { mb[i] = int(numAny(map[string]any{"v": v}, "v", 0)) }
		}
	}
	return gb, mb
}

// playersOnline 在线玩家列表（v1.9.42）：ws Hub 权威在线连接（15s 心跳窗口）。
func playersOnline(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, map[string]any{"players": OnlinePlayers(), "ts": time.Now().UnixMilli()})
}

// playerProfile 玩家公开档案（v1.9.42）：点其他玩家查看。只暴露公开信息，不含仓库/资源。
func playerProfile(w http.ResponseWriter, r *http.Request) {
	uid := r.URL.Query().Get("userId")
	if uid == "" {
		http.Error(w, "userId 不能为空", 400); return
	}
	p, err := disk.Load(uid)
	if err != nil || p == nil {
		http.Error(w, "玩家不存在或尚未创建角色", 404); return
	}
	equips := []map[string]any{}
	for _, e := range p.Equip {
		if !e.Equipped { continue }
		equips = append(equips, map[string]any{
			"id": e.ID, "slot": e.Slot, "quality": e.Quality, "enhance": e.Enhance,
			"stats": e.Stats, "classId": e.ClassID,
		})
	}
	gname, grole := "", ""
	if g, role := store.Guilds().GuildByUser(uid); g != nil {
		gname, grole = g.Name, role
	}
	online := false
	for _, op := range OnlinePlayers() {
		if op["userId"] == uid { online = true; break }
	}
	jsonOut(w, map[string]any{
		"userId": uid, "name": p.Core.Name, "level": p.Core.Level,
		"classId": p.Core.ClassID, "power": p.Core.BattlePower,
		"guild": gname, "guildRole": grole, "online": online,
		"equips": equips,
	})
}

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
	if err := store.InitGuilds(dataDir); err != nil {
		panic("公会数据初始化失败: " + err.Error())
	}
	game.InitSpawns()
	mux := http.NewServeMux()
	mux.HandleFunc("/health", health)
	mux.HandleFunc("/api/config", getConfig)
	mux.HandleFunc("/api/tables", tables)

	mux.HandleFunc("/api/character/new", characterNew)
	mux.HandleFunc("/api/state/load", stateLoad)
	mux.HandleFunc("/api/player/create_character", createCharacter) // 角色创建（服务端权威）

	// 账号体系（Nakama 语义同构）
	mux.HandleFunc("/api/auth/device", deviceLogin)
	mux.HandleFunc("/api/auth/register", registerAccount)
	mux.HandleFunc("/api/auth/login", loginAccount)
	mux.HandleFunc("/api/auth/bind-device", bindDevice)

	// 世界/点位/宝箱权威接口
	mux.HandleFunc("/api/chest/open", chestOpen)
	mux.HandleFunc("/api/chest/drop", chestDrop) // v1.9.45 怪物掉落宝箱登记（服务端掷档）
	mux.HandleFunc("/api/potion/pick", potionPick) // v1.9.27 采集地图草药（回血药水）
	mux.HandleFunc("/api/monster/kill", monsterKill)
	mux.HandleFunc("/api/guild/list", guildList)
	mux.HandleFunc("/api/guild/create", guildCreate)
	mux.HandleFunc("/api/guild/join", guildJoin)
	mux.HandleFunc("/api/guild/leave", guildLeave)
	mux.HandleFunc("/api/spawn", spawnQuery)
	mux.HandleFunc("/api/world/unlocks", worldUnlocks)
	mux.HandleFunc("/api/world/travel", worldTravel)

	// 实时多人（同端口 WebSocket）
	mux.HandleFunc("/api/players/online", playersOnline)  // v1.9.42 在线玩家列表
	mux.HandleFunc("/api/player/profile", playerProfile)  // v1.9.42 他人玩家档案
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
		"creator": c.Creator,
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
	p.Core.HP = maxHpOf(p) // v1.9.27 满血开局
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
	// v1.9.7：技能表按交付包重写 + appearance 补齐（老玩家不会被迫重新创建角色）
	if p.Migrate() {
		migrated = true
	}
	// v1.9.38 武器系归一：老档武器按当前职业归入对应系（登录读档即平滑生效）
	if p.MigrateWeaponClass() {
		migrated = true
	}
	if migrated {
		_ = disk.Save(q.UserID, p)
	}
	jsonOut(w, map[string]any{"exists": true, "state": p})
}

// createCharacter 角色创建（交付包 C→S create_character）：服务端权威校验后
// 写 appearance + classId + 初始技能，并置 created=true。
func createCharacter(w http.ResponseWriter, r *http.Request) {
	var q struct {
		UserID   string            `json:"userId"`
		Name     string            `json:"name"`
		ClassID  string            `json:"classId"`
		HomeLook map[string]string `json:"homeLook"`
		Skills   []string          `json:"skills"`
	}
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	uid, ok := authUserID(r)
	if !ok && q.UserID != "" {
		uid = q.UserID
	}
	if uid == "" {
		http.Error(w, "缺少 userId", 400)
		return
	}
	p, err := loadOrCreate(uid)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if err := p.CreateCharacter(q.ClassID, q.HomeLook, q.Name, q.Skills); err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	p.Core.HP = maxHpOf(p) // v1.9.27 新角色满血开局（落盘）
	if err := disk.Save(uid, p); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	jsonOut(w, map[string]any{"state": p, "appearance": p.Appearance})
}

// ---------- 纯结算（不落库，回放/验证用）----------

// calcEquipItem 装备入参：v1.9.28 起可携带 equipped 标记——
// 服务端只把"已穿戴"的计入总属性，但每件的战力贡献（equipPowers）全部返回，
// 这样背包/仓库里未穿戴的装备也能显示战力。
type calcEquipItem struct {
	game.EquipItem
	Equipped *bool `json:"equipped"`
}

type calcStatsReq struct {
	Level   int             `json:"level"`
	ClassID string          `json:"classId"`
	Equip   []calcEquipItem `json:"equip"`
	Avatar  *game.Avatar    `json:"avatar"` // 旧格式兼容
	Items   []game.EquipItem `json:"items"` // 旧格式兼容
}

// calcStatsResp 总属性（扁平字段）+ 每件装备战力。
type calcStatsResp struct {
	game.Stats
	EquipPowers []game.EquipPower `json:"equipPowers"`
}

func calcStats(w http.ResponseWriter, r *http.Request) {
	// v1.9.20 兼容两种请求格式：
	//   新格式（正确）：{level, classId, equip:[...]}           → 直接解码
	//   旧格式（历史bug）：{avatar:{level,classId},items:[...]} → 拆包后计算，避免老缓存客户端拿到 0 级裸身属性
	body, _ := io.ReadAll(r.Body)
	var q calcStatsReq
	if err := json.Unmarshal(body, &q); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	a := game.Avatar{Level: q.Level, ClassID: q.ClassID}
	for _, it := range q.Equip {
		a.Equip = append(a.Equip, it.EquipItem)
	}
	if a.Level == 0 && a.ClassID == "" && len(a.Equip) == 0 && q.Avatar != nil {
		a = *q.Avatar
		if len(a.Equip) == 0 && len(q.Items) > 0 {
			a.Equip = q.Items
		}
	}
	st := game.CalcStats(a)

	// v1.9.28：全量装备逐件折算战力；带 equipped=false 标记的件不计入总属性
	resp := calcStatsResp{Stats: st, EquipPowers: []game.EquipPower{}}
	hasFlag := false
	for _, it := range q.Equip {
		if it.Equipped != nil {
			hasFlag = true
			break
		}
	}
	if hasFlag {
		worn := make([]game.EquipItem, 0, len(q.Equip))
		for _, it := range q.Equip {
			resp.EquipPowers = append(resp.EquipPowers, game.EquipPower{ID: it.ID, Power: game.EquipContribution(a.ClassID, it.EquipItem)})
			if it.Equipped != nil && *it.Equipped {
				worn = append(worn, it.EquipItem)
			}
		}
		st2 := game.CalcStats(game.Avatar{Level: a.Level, ClassID: a.ClassID, Equip: worn})
		resp.Stats = st2
	} else {
		for _, it := range a.Equip {
			resp.EquipPowers = append(resp.EquipPowers, game.EquipPower{ID: it.ID, Power: game.EquipContribution(a.ClassID, it)})
		}
	}
	jsonOut(w, resp)
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
		WorldID   string   `json:"worldId"` // v1.9.36 群怪：大世界点位战斗必传
		NodeID    string   `json:"nodeId"`  // v1.9.36 群怪：点位 ID，服务端按点位组合权威结算
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
	// 武器解封 + 职业池校验：等级不足/跨职业的技能自动剔除（服务端裁定）
	bd := filterBD(p, q.BD)
	req := game.BattleRequest{
		UserID: q.UserID, Avatar: game.Avatar{Level: p.Core.Level, ClassID: p.Core.ClassID, Equip: equip},
		BD: bd, MonsterID: q.MonsterID, MonsterLv: q.MonsterLv, Elite: q.Elite,
		StartHP: p.Core.HP, // v1.9.27 全局血量：以当前血量开局，战斗后余量写回存档
	}
	// v1.9.36 群怪模版：带点位的大世界战斗 → 服务端取点位当前组合结算（与遭遇贴图同源，所见即所战）
	if q.WorldID != "" && q.NodeID != "" {
		if g, ok := game.SM().MonsterGroup(q.WorldID, q.NodeID); ok {
			req.Group = g
			req.MonsterID = g[0] // 主怪（精英倍率/奖励/掉落语义锚点）
		}
	}
	rep := game.ResolveBattle(req)
	logs := store.ApplyBattleReport(p, rep)
	// v1.9.31 战败回家园：大世界战败后送回家园休整（记录来源世界，出门可返回）
	if rep.Result != "win" && p.World.WorldID != "HOME" {
		p.World.LastWorldID = p.World.WorldID
		p.World.WorldID = "HOME"
		p.World.X, p.World.Y = 1280, 780
		logs = append(logs, "你被击败，已回到家园休整（战败保底 1 血，用回血药水恢复后再出发）")
	}
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
	store.ApplyIdleResult(p, res, now, maxHpOf(p))
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
	oldCls := p.Core.ClassID
	old := make([]game.EquipItem, 0, len(p.Equip))
	for _, e := range p.Equip {
		old = append(old, game.EquipItem{ID: e.ID, Slot: e.Slot, Quality: e.Quality, Enhance: e.Enhance, Stats: e.Stats})
	}
	// 服务端权威：等级/阶段/路径校验 + 技能池重算（转职后原职业技能自动卸下）
	if err := p.TransferTo(q.To, len(p.World.Unlocked)); err != nil {
		http.Error(w, err.Error(), 403)
		return
	}
	// v1.9.30 双向换算：按 新/旧职业系数比值 折算，互转数值精确还原
	converted := game.TransferEquip(old, oldCls, q.To)
	for i := range p.Equip {
		p.Equip[i].Stats = converted[i].Stats
	}
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
		UserID   string            `json:"userId"`
		Action   string            `json:"action"`
		Slots    []string          `json:"slots"`    // submit_bd
		SkillID  string            `json:"skillId"`  // unlock_skill
		ItemID   string            `json:"itemId"`   // equip/unequip/deposit/withdraw/storage_decompose
		All      bool              `json:"all"`      // storage_decompose 批量分解全部
		Qty      int               `json:"qty"`      // deposit/withdraw
		Slot     string            `json:"slot"`     // enhance
		DGID     string            `json:"dungeonId"`
		QuestID  string            `json:"questId"`
		HomeLook map[string]string `json:"homeLook"` // update_look
		To       string            `json:"to"`       // class_advance
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
	case "update_look":
		// 家园换装（服务端权威：只改 appearance，不碰职业/装备/技能）
		look, e := p.UpdateLook(q.HomeLook)
		if e != nil { http.Error(w, e.Error(), 400); return }
		extra["homeLook"] = look
		p.Logs = append(p.Logs, store.LogEntry{Event: "更换家园外观"})
	case "class_advance":
		// 进阶（交付包 C→S class_advance）：等级 44 + 已开启第 2 世界
		oldCls := p.Core.ClassID
		old := make([]game.EquipItem, 0, len(p.Equip))
		for _, e := range p.Equip {
			old = append(old, game.EquipItem{ID: e.ID, Slot: e.Slot, Quality: e.Quality, Enhance: e.Enhance, Stats: e.Stats})
		}
		if e := p.TransferTo(q.To, len(p.World.Unlocked)); e != nil {
			http.Error(w, e.Error(), 403)
			return
		}
		// v1.9.30 双向换算：按 新/旧职业系数比值 折算，互转数值精确还原
		converted := game.TransferEquip(old, oldCls, q.To)
		for i := range p.Equip {
			p.Equip[i].Stats = converted[i].Stats
		}
		// v1.9.38 武器分系：新职业与已穿武器不同系 → 自动卸下（防具/饰品通用不受影响）
		newWC := game.WeaponClassFor(q.To)
		for i := range p.Equip {
			if it := p.Equip[i]; it.Equipped && it.Slot == "weapon" && it.ClassID != "" && it.ClassID != newWC {
				p.Equip[i].Equipped = false
				p.Logs = append(p.Logs, store.LogEntry{Event: fmt.Sprintf("武器「%s」为%s系专属，转职后已自动卸下", it.ID, game.WeaponClassCN(it.ClassID))})
			}
		}
		extra["classId"] = q.To
		extra["from"] = oldCls
		// v1.9.26 按转职类型给出不同文案（基础互转 / 进阶 / 回退）
		c := game.C()
		word := "转职"
		if cp, ok := c.Skills.Classes[q.To]; ok && cp.Tier == 1 {
			word = "进阶"
		} else if cp, ok := c.Skills.Classes[oldCls]; ok && cp.Tier == 1 {
			word = "回退"
		}
		extra["message"] = word + "成功，装备属性已按新职业系数换算"
		p.Logs = append(p.Logs, store.LogEntry{Event: fmt.Sprintf("职业%s：%s → %s", word, oldCls, q.To)})
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
		// v1.9.20/21：消耗按配置公式 costBase × costGrowth^当前强化等级 递增；
		// v1.9.21 支持按装备实例 ID（itemId）精确定位，避免同槽位多件装备强化错对象。
		enh := game.C().Equipment.Enhance
		goldBase := 1200
		if v, ok := enh["costBase"].(float64); ok { goldBase = int(v) }
		growth := 1.35
		if v, ok := enh["costGrowth"].(float64); ok { growth = v }
		matCost := 1 // v1.9.41 由 2 下调
		curLv := 0
		for _, e := range p.Equip {
			if (q.ItemID != "" && e.ID == q.ItemID) || (q.ItemID == "" && e.Slot == q.Slot) { curLv = e.Enhance; break }
		}
		goldCost := int(float64(goldBase) * math.Pow(growth, float64(curLv)))
		lvl, e := p.EnhanceEquip(q.ItemID, q.Slot, goldCost, matCost)
		if e != nil { http.Error(w, e.Error(), 400); return }
		extra["enhanceLevel"] = lvl
		extra["enhanceCost"] = map[string]int{"gold": goldCost, "material": matCost}
	case "quality_up": // v1.9.42 装备升品：品质+1（最高混沌），按新档分解基准折算消耗
		mults := []float64{}
		for _, qd := range game.C().Equipment.Quality {
			mults = append(mults, qd.Mult)
		}
		cfg, _ := game.C().Economy.Drop["qualityUp"].(map[string]any)
		goldMult, matMult := 1.2, 0.6
		if cfg != nil {
			if v, ok := cfg["goldMult"].(float64); ok { goldMult = v }
			if v, ok := cfg["matMult"].(float64); ok { matMult = v }
		}
		gb, mb := decomposeTables()
		// 目标新品质档（当前+1）的基准值决定消耗
		curQ := -1
		for i := range p.Equip {
			if p.Equip[i].ID == q.ItemID { curQ = p.Equip[i].Quality; break }
		}
		if curQ < 0 {
			for i := range p.Warehouse {
				if p.Warehouse[i].Kind == "equip" && p.Warehouse[i].ID == q.ItemID && p.Warehouse[i].Equip != nil {
					curQ = p.Warehouse[i].Equip.Quality; break
				}
			}
		}
		if curQ < 0 { http.Error(w, "未找到装备："+q.ItemID, 400); return }
		newQ := curQ + 1
		if newQ >= len(gb) { http.Error(w, "已达最高品质（混沌），无法继续升品", 409); return }
		goldCost := int(float64(gb[newQ]) * goldMult)
		matCost := int(float64(mb[newQ]) * matMult)
		newQ2, _, e := p.QualityUp(q.ItemID, goldCost, matCost, mults)
		if e != nil { http.Error(w, e.Error(), 400); return }
		extra["quality"] = newQ2
		extra["cost"] = map[string]int{"gold": goldCost, "material": matCost}
		extra["gold"] = p.Core.Gold
		extra["material"] = p.Core.Material
	case "use_potion":
		// v1.9.27 使用回血药水：服务端权威校验持有/满血，回血量按配置百分比
		potCfg, _ := game.C().Economy.Drop["potion"].(map[string]any)
		healPct := 0.35
		if m, ok := potCfg[q.ItemID].(map[string]any); ok {
			if v, ok := m["healPct"].(float64); ok && v > 0 && v <= 1 { healPct = v }
		}
		healed, e := p.UsePotion(q.ItemID, healPct, maxHpOf(p))
		if e != nil { http.Error(w, e.Error(), 400); return }
		extra["healed"] = healed
		extra["hp"] = p.Core.HP
		extra["maxHp"] = maxHpOf(p)
	case "deposit":
		if err = p.WarehouseDeposit(q.ItemID, q.Qty); err != nil { http.Error(w, err.Error(), 400); return }
	case "withdraw":
		if err = p.WarehouseWithdraw(q.ItemID, q.Qty); err != nil { http.Error(w, err.Error(), 400); return }
	case "wh_equip_in": // 背包装备 → 仓库（点击背包装备存入）
		if err = p.WarehouseEquipIn(q.ItemID); err != nil { http.Error(w, err.Error(), 400); return }
	case "wh_equip_out": // 仓库装备 → 背包（点击仓库装备取出）
		if err = p.WarehouseEquipOut(q.ItemID); err != nil { http.Error(w, err.Error(), 400); return }
	case "storage_decompose": // v1.9.40 分解仓库装备 / v1.9.41 加材料返还 + 批量分解
		enhMult := 0.15
		if cfg, ok := game.C().Economy.Drop["decompose"].(map[string]any); ok {
			if v, ok := cfg["enhanceMult"].(float64); ok { enhMult = v }
		}
		base := []any{}   // 兼容下方 target 品质取值逻辑
		matBase := []any{}
		gb, mb := decomposeTables()
		for _, v := range gb { base = append(base, float64(v)) }
		for _, v := range mb { matBase = append(matBase, float64(v)) }
		store.SetDecomposeConfig(gb, mb, enhMult)
		if q.All { // 批量：分解仓库全部装备
			n, gold, mat := p.WarehouseDecomposeAll()
			if n == 0 { http.Error(w, "仓库中没有装备可分解", 400); return }
			extra["decomposed"] = n
			extra["goldGain"] = gold
			extra["matGain"] = mat
			extra["gold"] = p.Core.Gold
			extra["material"] = p.Core.Material
			break
		}
		var target *store.Equip
		for i := range p.Warehouse {
			it := p.Warehouse[i]
			if it.Kind == "equip" && it.ID == q.ItemID {
				target = it.Equip
				break
			}
		}
		qi, goldGain, matGain := 0, 40, 10
		if target != nil {
			qi = target.Quality
			if qi < 0 || qi >= len(base) { qi = 0 }
			g, _ := base[qi].(float64)
			m, _ := matBase[qi].(float64)
			goldGain = int(g * (1 + float64(target.Enhance)*enhMult))
			matGain = int(m)
		}
		gGain, mGain, e := p.WarehouseDecompose(q.ItemID, goldGain, matGain)
		if e != nil { http.Error(w, e.Error(), 400); return }
		extra["goldGain"] = gGain
		extra["matGain"] = mGain
		extra["gold"] = p.Core.Gold
		extra["material"] = p.Core.Material
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
		if p.Guild == nil { http.Error(w, "尚未加入公会", 409); return }
		if p.Ext == nil { p.Ext = map[string]any{} }
		today := time.Now().Format("2006-01-02")
		if d, _ := p.Ext["guildDay"].(string); d != today {
			p.Ext["guildDay"] = today
			p.Ext["guildDonated"] = float64(0)
		}
		donated := 0
		if v, ok := p.Ext["guildDonated"].(float64); ok { donated = int(v) }
		if donated >= 3 { http.Error(w, "今日捐献次数已用完（3/3）", 409); return }
		const cost = 600 // v1.9.41 由 1000 下调
		if p.Core.Gold < cost { http.Error(w, "金币不足（需 1,000）", 400); return }
		p.Core.Gold -= cost
		coin := 0
		if v, ok := p.Ext["guildCoin"].(float64); ok { coin = int(v) }
		coin += 50
		p.Ext["guildCoin"] = float64(coin)
		p.Ext["guildDonated"] = float64(donated + 1)
		p.GainExp(60)
		// v1.9.18：公会进度 +50，成员等级/战力快照同步进注册表（服务端权威）
		progress, _, derr := store.Guilds().GuildDonate(p.Guild.ID, p.UserID, p.Core.Level, p.Core.BattlePower)
		if derr != nil { http.Error(w, derr.Error(), 409); return }
		extra["guildCoin"] = coin
		extra["guildDonated"] = donated + 1
		extra["guildProgress"] = progress
	case "guild_buy":
		if p.Guild == nil { http.Error(w, "尚未加入公会", 409); return }
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
		// 家园装饰升级（服务端权威：定价 + 扣费 + 等级上限，前端只报 itemId）
		item := q.ItemID
		var cost, lv int
		switch item {
		case "cart":
			lv = p.Home.Cart
			cost = 550 * (lv + 1) // 550/1100/1650...（v1.9.41 由 800 下调）
		case "bed":
			lv = p.Home.Bed
			cost = 700 * (lv + 1) // v1.9.41 由 1000 下调
		default:
			http.Error(w, "未知装饰", 400)
			return
		}
		if lv >= 20 {
			http.Error(w, "已达最高等级", 409)
			return
		}
		if p.Core.Gold < cost {
			http.Error(w, fmt.Sprintf("金币不足（需 %d）", cost), 400)
			return
		}
		p.Core.Gold -= cost
		if item == "cart" {
			p.Home.Cart++
		} else {
			p.Home.Bed++
		}
		extra["cost"] = cost
		p.Logs = append(p.Logs, store.LogEntry{Event: fmt.Sprintf("家园装饰升级：%s Lv.%d（-%d 金币）", item, lv+1, cost)})
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
	// v1.9.27 老档兼容：HP 字段缺省/为 0 → 视为满血回填（战斗前血量兜底）
	if p.Appearance.Created && p.Core.HP <= 0 {
		p.Core.HP = maxHpOf(p)
	}
	// v1.9.38 老档武器系归一：存量武器按当前职业归入对应系（变更才落盘，避免每请求写盘）
	if p.MigrateWeaponClass() {
		if err := disk.Save(userID, p); err != nil {
			return nil, err
		}
	}
	return p, nil
}

// maxHpOf 服务端权威计算角色最大血量（按等级+穿戴装备）。
func maxHpOf(p *store.PlayerState) float64 {
	equip := make([]game.EquipItem, 0, len(p.Equip))
	for _, e := range p.Equip {
		if e.Equipped {
			equip = append(equip, game.EquipItem{ID: e.ID, Slot: e.Slot, Quality: e.Quality, Enhance: e.Enhance, Stats: e.Stats})
		}
	}
	return game.CalcStats(game.Avatar{Level: p.Core.Level, ClassID: p.Core.ClassID, Equip: equip}).HP
}

// filterBD 服务端二次校验：只保留当前职业技能池内、且已解锁的技能（防客户端篡改）。
// 若一个都不剩，回退到该职业初始技能，保证战斗不会空转。
func filterBD(p *store.PlayerState, bd []string) []string {
	c := game.C()
	unlocked := map[string]bool{}
	for _, id := range p.Skills.Unlocked {
		unlocked[id] = true
	}
	out := []string{}
	for _, id := range bd {
		if id == "basic" || id == "" {
			continue
		}
		if !unlocked[id] {
			continue
		}
		if !c.InBDPool(p.Core.ClassID, id) {
			continue
		}
		out = append(out, id)
		if len(out) >= 4 {
			break
		}
	}
	if len(out) == 0 {
		pool := c.SkillPoolOf(p.Core.ClassID)
		out = append(out, pool.Initial...)
	}
	return out
}

func index(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store") // HTML 强制不缓存，确保每次拿到最新脚本引用（v1.9.24 补充）
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
