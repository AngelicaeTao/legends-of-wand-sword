// Package net 提供 HTTP API（standalone 初跑模式），带磁盘存档持久化。
// 生产模式用 Nakama RPC 调用同样的 game/store 包逻辑。
package net

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"zjcs/server/internal/game"
	"zjcs/server/internal/store"
)

var disk *store.DiskStore
var webFile string   // 可选：前端单文件页面路径，非空时由 "/" 托管
var webResDir string // 可选：静态资源目录（png 等），由 /res/ 托管

// Handler 聚合所有 API 路由（兼容旧调用，不托管前端）。
func Handler(cfgDir string) http.Handler {
	return HandlerWithWeb(cfgDir, "", "")
}

// HandlerWithWeb 聚合所有 API 路由，并可托管前端页面与静态资源（同源部署，单端口）。
// FlushSaves 优雅关闭前刷盘（v1.9.41 异步落盘配套）。
func FlushSaves() error {
	if disk == nil {
		return nil
	}
	return disk.FlushAll()
}

// decomposeTables 从 economy.drop.decompose 读品质基准表（单件分解与升品共用）。
func decomposeTables() (goldBase, matBase []int) {
	gb := []int{40, 120, 300, 800, 2000, 4200, 8000, 15000}
	mb := []int{10, 30, 80, 200, 500, 1200, 2600, 6000}
	if cfg, ok := game.C().Economy.Drop["decompose"].(map[string]any); ok {
		if b, ok := cfg["goldBase"].([]any); ok && len(b) > 0 {
			gb = make([]int, len(b))
			for i, v := range b {
				gb[i] = int(numAny(map[string]any{"v": v}, "v", 0))
			}
		}
		if b, ok := cfg["matBase"].([]any); ok && len(b) > 0 {
			mb = make([]int, len(b))
			for i, v := range b {
				mb[i] = int(numAny(map[string]any{"v": v}, "v", 0))
			}
		}
	}
	return gb, mb
}

// playersOnline 在线玩家列表（v1.9.42）：ws Hub 权威在线连接（15s 心跳窗口）。
func playersOnline(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, map[string]any{"players": OnlinePlayers(), "ts": time.Now().UnixMilli()})
}

// playerProfile 玩家公开档案（v1.9.42）：点其他玩家查看。只暴露公开信息，不含仓库/资源。
// v1.9.61 补全：原先只回等级/战力/穿戴列表，档案页缺属性；现在给出完整属性与逐件战力。
func playerProfile(w http.ResponseWriter, r *http.Request) {
	uid := r.URL.Query().Get("userId")
	if uid == "" {
		http.Error(w, "userId 不能为空", 400)
		return
	}
	p, err := disk.Load(uid)
	if err != nil || p == nil {
		http.Error(w, "玩家不存在或尚未创建角色", 404)
		return
	}
	equips := []map[string]any{}
	worn := make([]game.EquipItem, 0, 4)
	for _, e := range p.Equip {
		if !e.Equipped {
			continue
		}
		gi := game.EquipItem{ID: e.ID, Slot: e.Slot, Quality: e.Quality, Enhance: e.Enhance, Stats: e.Stats, ClassID: e.ClassID}
		worn = append(worn, gi)
		equips = append(equips, map[string]any{
			"id": e.ID, "slot": e.Slot, "quality": e.Quality, "enhance": e.Enhance,
			"stats": e.Stats, "classId": e.ClassID,
			"power": int(game.EquipContribution(p.Core.ClassID, gi)), // v1.9.61 单件战力
		})
	}
	// v1.9.89 与 /api/stats/calc 同口径：带技能与被动，避免面板战力与属性接口对不上
	st := game.CalcStats(game.Avatar{Level: p.Core.Level, ClassID: p.Core.ClassID, Equip: worn,
		Skills: p.Skills.Slots, SkillLv: p.Skills.Levels, Passives: p.Skills.Passives})
	gname, grole := "", ""
	if g, role := store.Guilds().GuildByUser(uid); g != nil {
		gname, grole = g.Name, role
	}
	online := false
	for _, op := range OnlinePlayers() {
		if op["userId"] == uid {
			online = true
			break
		}
	}
	jsonOut(w, map[string]any{
		"userId": uid, "name": p.Core.Name, "level": p.Core.Level,
		"classId": p.Core.ClassID, "power": int(st.Power),
		"guild": gname, "guildRole": grole, "online": online,
		"worldId": p.World.WorldID,
		// v1.9.61 档案页要能看全属性，不再只有等级和战力
		"hp": st.HP, "atk": st.ATK, "def": st.DEF, "crit": st.Crit, "spd": st.Spd,
		"bagCount": len(p.Equip), "equips": equips,
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
	startPartyJanitor()
	startBackupLoop(dataDir)
	mux := http.NewServeMux()
	mux.HandleFunc("/health", health)
	mux.HandleFunc("/api/health", health)
	mux.HandleFunc("/api/config", getConfig)
	mux.HandleFunc("/api/tables", tables)

	mux.HandleFunc("/api/character/new", characterNew)
	mux.HandleFunc("/api/state/load", stateLoad)
	mux.HandleFunc("/api/player/create_character", createCharacter) // 角色创建（服务端权威）

	// 账号体系（Nakama 语义同构）；登录/注册/设备登录按 IP 限流
	mux.HandleFunc("/api/auth/device", rateLimit(deviceLogin))
	mux.HandleFunc("/api/auth/register", rateLimit(registerAccount))
	mux.HandleFunc("/api/auth/login", rateLimit(loginAccount))
	mux.HandleFunc("/api/auth/bind-device", bindDevice)

	// 世界/点位/宝箱权威接口
	mux.HandleFunc("/api/chest/open", chestOpen)
	mux.HandleFunc("/api/chest/drop", chestDrop)   // v1.9.45 怪物掉落宝箱登记（服务端掷档）
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
	// v1.9.68 组队副本：大厅 / 队伍 / 邀请 / 开始
	mux.HandleFunc("/api/party/list", partyList)
	mux.HandleFunc("/api/party/create", partyCreate)
	mux.HandleFunc("/api/party/join", partyJoin)
	mux.HandleFunc("/api/party/leave", partyLeave)
	mux.HandleFunc("/api/party/kick", partyKick)
	mux.HandleFunc("/api/party/invite", partyInvite)
	mux.HandleFunc("/api/party/accept", partyAccept)
	mux.HandleFunc("/api/party/decline", partyDecline)
	mux.HandleFunc("/api/party/status", partyStatus)
	mux.HandleFunc("/api/party/start", partyStart)
	mux.HandleFunc("/api/dungeon/party/info", partyDungeonInfo)
	mux.HandleFunc("/api/players/online", playersOnline) // v1.9.42 在线玩家列表
	mux.HandleFunc("/api/player/profile", playerProfile) // v1.9.42 他人玩家档案
	mux.HandleFunc("/ws", wsHandler)

	mux.HandleFunc("/api/stats/calc", calcStats)
	mux.HandleFunc("/api/battle/resolve", resolveBattle) // 纯结算（不落库，便于回放）
	mux.HandleFunc("/api/battle/fight", battleFight)     // 战斗 + 落库
	mux.HandleFunc("/api/idle/settle", idleSettle)       // 纯结算
	mux.HandleFunc("/api/idle/finalize", idleFinalize)   // 离线 + 落库
	mux.HandleFunc("/api/transfer", transferClass)       // 转职 + 落库
	mux.HandleFunc("/api/player/action", playerAction)   // BD/装备/仓库/副本/家园 权威操作 + 落库

	if webResDir != "" {
		// v1.9.60 交互/加载延迟优化：资源引用都带 ?v= 版本号（index.html 每次发版都提版本），
		// 因此可以放心长缓存——公网隧道单次往返 0.6~1.4s，原来每个 JS/CSS 都要回源校验一次。
		// index.html 仍是 no-store，保证版本号本身不会吃到旧值。
		fs := http.StripPrefix("/res/", http.FileServer(http.Dir(webResDir)))
		mux.Handle("/res/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("v") != "" {
				w.Header().Set("Cache-Control", "public, max-age=86400, stale-while-revalidate=604800")
			} else {
				// 无版本号的直接访问（调试/老页面）仍然回源校验，避免拿到过期内容
				w.Header().Set("Cache-Control", "no-cache")
			}
			fs.ServeHTTP(w, r)
		}))
	}
	mux.HandleFunc("/", index)
	// 顺序：限制请求体 → CORS → 统一鉴权 → 业务路由；管理入口独立校验本机直连。
	publicHandler := bodyLimit(cors(requireAuth(mux)))
	adminHandler := newAdminHandler(accounts)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/admin" || strings.HasPrefix(r.URL.Path, "/admin/") {
			adminHandler.ServeHTTP(w, r)
			return
		}
		publicHandler.ServeHTTP(w, r)
	})
}

// maxBodyBytes 全局请求体上限（1MB）。
const maxBodyBytes = 1 << 20

// bodyLimit 用 http.MaxBytesReader 限制所有请求体大小。
func bodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}

// publicAPIPaths 无需鉴权的 API 路径白名单（其余 /api/* 一律要求 token）。
var publicAPIPaths = map[string]bool{
	"/api/health":        true,
	"/api/config":        true,
	"/api/tables":        true,
	"/api/auth/device":   true,
	"/api/auth/register": true,
	"/api/auth/login":    true,
}

// requireAuth 统一鉴权中间件：所有 /api/* 除白名单外都必须带有效 Bearer token。
// 静态资源（/res/）、首页与 /ws（WS 单独做握手鉴权）不在此拦截。
func requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if !strings.HasPrefix(p, "/api/") || publicAPIPaths[p] {
			next.ServeHTTP(w, r)
			return
		}
		if _, ok := authUserIDHeader(r); !ok {
			http.Error(w, "登录状态已失效，请重新登录", 401)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---------- 登录接口限流（按客户端 IP，每 IP 每分钟 20 次） ----------

const rateLimitPerMin = 20

type rateRec struct {
	count int
	start time.Time
}

var (
	rateMu   sync.Mutex
	rateHits = map[string]*rateRec{}
)

func clientIP(r *http.Request) string {
	// 线上经 Cloudflare 隧道转发：优先取真实客户端 IP，直连时回落到 RemoteAddr。
	if ip := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); ip != "" {
		return ip
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if first := strings.TrimSpace(strings.Split(xff, ",")[0]); first != "" {
			return first
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// rateLimit 对登录/注册类接口做简单固定窗口限流，超限返回 429。
func rateLimit(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		now := time.Now()
		rateMu.Lock()
		rec := rateHits[ip]
		if rec == nil || now.Sub(rec.start) >= time.Minute {
			rec = &rateRec{start: now}
			rateHits[ip] = rec
		}
		rec.count++
		over := rec.count > rateLimitPerMin
		// 顺手清理过期的窗口，防止 map 无限增长
		if len(rateHits) > 4096 {
			for k, v := range rateHits {
				if now.Sub(v.start) >= time.Minute {
					delete(rateHits, k)
				}
			}
		}
		rateMu.Unlock()
		if over {
			http.Error(w, "请求过于频繁，请稍后再试", 429)
			return
		}
		next(w, r)
	}
}

// internalErr 服务端内部错误：只记日志，不把底层错误回显给客户端。
func internalErr(w http.ResponseWriter, err error) {
	log.Printf("[internal] %v", err)
	http.Error(w, "服务内部错误，请稍后重试", 500)
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 只对同源请求回显 Origin，不再使用 Access-Control-Allow-Origin: *
		origin := r.Header.Get("Origin")
		if origin != "" && sameOrigin(origin, r.Host) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// sameOrigin 判断 Origin 是否与请求 Host 同源（协议不比较，端口/主机一致即可）。
func sameOrigin(origin, host string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, host)
}

func jsonOut(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func health(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, map[string]any{"ok": true, "service": "zjcs-authoritative-server", "mode": "standalone", "persist": "disk"})
}

func getConfig(w http.ResponseWriter, r *http.Request) {
	// 配置会随版本更新（世界尺寸/点位/BOSS 解锁等），禁止浏览器启发式缓存。
	w.Header().Set("Cache-Control", "no-store")
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
	uid, ok := mustUser(w, r, q.UserID)
	if !ok {
		return
	}
	p := store.NewPlayer(uid, q.Name, q.ClassID)
	p.Core.HP = maxHpOf(p) // v1.9.27 满血开局
	if err := disk.Save(uid, p); err != nil {
		internalErr(w, err)
		return
	}
	jsonOut(w, p)
}

func stateLoad(w http.ResponseWriter, r *http.Request) {
	var q struct{ UserID string }
	_ = json.NewDecoder(r.Body).Decode(&q)
	uid, ok := mustUser(w, r, q.UserID)
	if !ok {
		return
	}
	p, err := disk.Load(uid)
	if err != nil {
		internalErr(w, err)
		return
	}
	if p == nil {
		jsonOut(w, map[string]any{"exists": false})
		return
	}
	// 老存档迁移：非 W1 世界沿用 v1.8 的 15360→7680 折半；
	// v2.0 W1 已扩到 11520×6480，超出新边界的存档直接落回新安全点（客户端还会就近修正落墙）。
	migrated := false
	if p.World.WorldID == "W1" {
		if p.World.X >= 3700 && p.World.X <= 4000 && p.World.Y >= 2050 && p.World.Y <= 2260 {
			p.World.X, p.World.Y = 1680, 3240
			migrated = true
		}
		if p.World.X < 32 || p.World.Y < 32 || p.World.X > 11488 || p.World.Y > 6448 {
			p.World.X, p.World.Y = 1680, 3240
			migrated = true
		}
	} else {
		if p.World.X > 7680 || p.World.Y > 4320 {
			p.World.X, p.World.Y = p.World.X/2, p.World.Y/2
			migrated = true
		}
		if p.World.X < 32 || p.World.Y < 32 || p.World.X > 7648 || p.World.Y > 4288 {
			p.World.X, p.World.Y = 2560, 2176
			migrated = true
		}
	}
	// v1.9.7：技能表按交付包重写 + appearance 补齐（老玩家不会被迫重新创建角色）
	if p.Migrate() {
		migrated = true
	}
	// v1.9.38 武器系归一：老档武器按当前职业归入对应系（登录读档即平滑生效）
	if p.MigrateWeaponClass() {
		migrated = true
	}
	// 每日任务按服务端日期懒重置；永久主线领取记录不受影响。
	if _, changed := ensureDailyQuests(p, game.ServerNow()); changed {
		migrated = true
	}
	if migrated {
		_ = disk.Save(uid, p)
	}
	jsonOut(w, map[string]any{"exists": true, "state": p, "stats": playerStats(p)})
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
	uid, ok := mustUser(w, r, q.UserID)
	if !ok {
		return
	}
	p, err := loadOrCreate(uid)
	if err != nil {
		internalErr(w, err)
		return
	}
	if err := p.CreateCharacter(q.ClassID, q.HomeLook, q.Name, q.Skills); err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	p.Core.HP = maxHpOf(p) // v1.9.27 新角色满血开局（落盘）
	if err := disk.Save(uid, p); err != nil {
		internalErr(w, err)
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
	Level   int              `json:"level"`
	ClassID string           `json:"classId"`
	Equip   []calcEquipItem  `json:"equip"`
	Avatar  *game.Avatar     `json:"avatar"` // 旧格式兼容
	Items   []game.EquipItem `json:"items"`  // 旧格式兼容
	// v1.9.85 未登录纯计算时可用；登录用户以存档为准，这三个字段被忽略
	Skills   []string       `json:"skills"`
	SkillLv  map[string]int `json:"skillLv"`
	Passives []string       `json:"passives"` // v1.9.89 被动技能
}

// calcStatsResp 总属性（扁平字段）+ 每件装备战力 + 技能战力明细（v1.9.85）。
type calcStatsResp struct {
	game.Stats
	EquipPowers []game.EquipPower      `json:"equipPowers"`
	SkillPowers []game.SkillPowerEntry `json:"skillPowers,omitempty"`
	SkillPower  float64                `json:"skillPower,omitempty"`
	// v1.9.89 被动技能战力：按 powerRule.base.passive 固定档位计分，与属性增幅不重复计算
	PassivePowers []game.SkillPowerEntry `json:"passivePowers,omitempty"`
	PassivePower  float64                `json:"passivePower,omitempty"`
}

// playerStats 用存档直接算属性，口径与 /api/stats/calc 一致。
// v1.9.60 交互延迟优化：原先前端每次操作后都要再请求一次 /api/stats/calc 重算属性，
// 公网单程 0.6~1.4 秒，等于每个操作都付两倍往返。现在把属性随操作响应一起返回。
func playerStats(p *store.PlayerState) map[string]any {
	worn := make([]game.EquipItem, 0, len(p.Equip))
	powers := make([]game.EquipPower, 0, len(p.Equip)+len(p.Warehouse))
	for _, e := range p.Equip {
		gi := game.EquipItem{ID: e.ID, Slot: e.Slot, Quality: e.Quality, Enhance: e.Enhance, Stats: e.Stats, ClassID: e.ClassID}
		if e.Equipped {
			worn = append(worn, gi)
		}
		powers = append(powers, game.EquipPower{ID: e.ID, Power: game.EquipContribution(p.Core.ClassID, gi)})
	}
	// 仓库里的装备只参与"逐件战力"展示，不计入已穿戴总属性（与前端 refreshStats 同口径）
	for _, it := range p.Warehouse {
		if it.Kind != "equip" || it.Equip == nil {
			continue
		}
		e := it.Equip
		gi := game.EquipItem{ID: e.ID, Slot: e.Slot, Quality: e.Quality, Enhance: e.Enhance, Stats: e.Stats, ClassID: e.ClassID}
		powers = append(powers, game.EquipPower{ID: e.ID, Power: game.EquipContribution(p.Core.ClassID, gi)})
	}
	st := game.CalcStats(game.Avatar{
		Level: p.Core.Level, ClassID: p.Core.ClassID, Equip: worn,
		Skills: p.Skills.Slots, SkillLv: p.Skills.Levels,
		Passives: p.Skills.Passives, // v1.9.89 被动属性 + 固定战力分
	})
	// v1.9.85 技能与技能等级计入战力：逐技能明细供面板显示，skillPower 为合计
	skillPowers := game.SkillPowers(p.Skills.Slots, p.Skills.Levels)
	return map[string]any{
		"hp": st.HP, "atk": st.ATK, "def": st.DEF, "crit": st.Crit, "spd": st.Spd, "power": st.Power,
		"equipPowers":   powers,
		"skillPowers":   skillPowers,
		"skillPower":    game.SkillPowerSum(p.Skills.Slots, p.Skills.Levels),
		"passivePowers": game.PassivePowers(p.Skills.Passives, p.Skills.Levels),
		"passivePower":  game.PassivePowerSum(p.Skills.Passives, p.Skills.Levels),
	}
}

// livePower 用当前等级+装备实时算战力。存档里的 Core.BattlePower 是"上次操作时"的快照，
// 老号/长期不操作的号会停在 0 或旧值，公会面板与档案一律以实时值为准（v1.9.61）。
func livePower(p *store.PlayerState) int {
	if p == nil {
		return 0
	}
	if v, ok := playerStats(p)["power"].(float64); ok {
		return int(v)
	}
	return 0
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
	// v1.9.85 技能与技能等级计入战力：登录用户一律以存档为准（客户端传什么都不作数），
	// 未登录的纯计算请求才用请求体里的 skills/skillLv（缺省=无技能战力，与老客户端兼容）。
	if uid, ok := authUserID(r); ok {
		if p, err := disk.Load(uid); err == nil && p != nil {
			a.Skills = p.Skills.Slots
			a.SkillLv = p.Skills.Levels
			a.Passives = p.Skills.Passives // v1.9.89 被动以存档为准
		}
	} else {
		if len(a.Skills) == 0 && len(q.Skills) > 0 {
			a.Skills = q.Skills
			a.SkillLv = q.SkillLv
		}
		if len(a.Passives) == 0 && len(q.Passives) > 0 {
			a.Passives = q.Passives
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
		st2 := game.CalcStats(game.Avatar{Level: a.Level, ClassID: a.ClassID, Equip: worn, Skills: a.Skills, SkillLv: a.SkillLv, Passives: a.Passives})
		resp.Stats = st2
	} else {
		for _, it := range a.Equip {
			resp.EquipPowers = append(resp.EquipPowers, game.EquipPower{ID: it.ID, Power: game.EquipContribution(a.ClassID, it)})
		}
	}
	// v1.9.85 技能战力明细（已装备技能 × 等级）
	resp.SkillPowers = game.SkillPowers(a.Skills, a.SkillLv)
	resp.SkillPower = game.SkillPowerSum(a.Skills, a.SkillLv)
	resp.PassivePowers = game.PassivePowers(a.Passives, a.SkillLv)
	resp.PassivePower = game.PassivePowerSum(a.Passives, a.SkillLv)
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

// playerProgress 推进进度：已解锁最高世界在 W1..W6 中的位置，归一化到 0~1（W1=0，W6=1）。
// v1.9.53 供首领动态强度使用，只认存档，不认客户端上报。
func playerProgress(p *store.PlayerState) float64 {
	best := 0
	consider := func(id string) {
		if len(id) < 2 || (id[0] != 'W' && id[0] != 'w') {
			return
		}
		if n, err := strconv.Atoi(id[1:]); err == nil && n > best {
			best = n
		}
	}
	consider(p.World.WorldID)
	for _, w := range p.World.Unlocked {
		consider(w)
	}
	if best <= 1 {
		return 0
	}
	if best >= 6 {
		return 1
	}
	return float64(best-1) / 5
}

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
	uid, ok := mustUser(w, r, q.UserID)
	if !ok {
		return
	}
	q.UserID = uid
	p, err := loadOrCreate(uid)
	if err != nil {
		internalErr(w, err)
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
	// v2.0：带点位的大世界战斗，BOSS 独立等级以配置为准（不被客户端传入的等级覆盖）
	if q.WorldID != "" && q.NodeID != "" {
		for _, wd := range game.C().Worlds.Worlds {
			if wd.ID != q.WorldID {
				continue
			}
			for i := range wd.Spawns {
				sp := &wd.Spawns[i]
				if sp.ID != q.NodeID {
					continue
				}
				if sp.Level > 0 {
					q.MonsterLv = sp.Level
				}
				if sp.BossMonster != "" {
					q.MonsterID = sp.BossMonster
				}
			}
		}
	}
	req := game.BattleRequest{
		UserID: q.UserID, Avatar: game.Avatar{Level: p.Core.Level, ClassID: p.Core.ClassID, Equip: equip,
			Passives: p.Skills.Passives}, // v1.9.89 被动特效以存档为准，不接受客户端传参
		BD: bd, MonsterID: q.MonsterID, MonsterLv: q.MonsterLv, Elite: q.Elite,
		StartHP: p.Core.HP,       // v1.9.27 全局血量：以当前血量开局，战斗后余量写回存档
		SkillLv: p.Skills.Levels, // v1.9.56 技能等级：每级效果 +8%，以存档为准
	}
	// v1.9.36 群怪模版：带点位的大世界战斗 → 服务端取点位当前组合结算（与遭遇贴图同源，所见即所战）
	if q.WorldID != "" && q.NodeID != "" {
		if g, ok := game.SM().MonsterGroup(q.WorldID, q.NodeID); ok {
			req.Group = g
			req.MonsterID = g[0] // 主怪（精英倍率/奖励/掉落语义锚点）
		}
	}
	// v1.9.53 推进进度以存档为准（不接受客户端传值）：首领强度据此缩放
	req.Progress = playerProgress(p)
	rep := game.ResolveBattle(req)
	logs := store.ApplyBattleReport(p, &rep)
	// v1.9.93 每日「今日讨伐」：仅胜利推进战斗进度（战败不计）
	if rep.Result == "win" {
		advanceDailyQuest(p, "battle", game.ServerNow())
	}
	// v1.9.31 战败回家园：大世界战败后送回家园休整（记录来源世界，出门可返回）
	if rep.Result != "win" && p.World.WorldID != "HOME" {
		p.World.LastWorldID = p.World.WorldID
		p.World.WorldID = "HOME"
		p.World.X, p.World.Y = 1280, 780
		logs = append(logs, "你被击败，已回到家园休整（战败保底 1 血，用回血药水恢复后再出发）")
	}
	if err := disk.Save(q.UserID, p); err != nil {
		internalErr(w, err)
		return
	}
	jsonOut(w, map[string]any{"report": rep, "logs": logs, "state": p, "stats": playerStats(p)})
}

func idleFinalize(w http.ResponseWriter, r *http.Request) {
	var q struct{ UserID string }
	_ = json.NewDecoder(r.Body).Decode(&q)
	uid, ok := mustUser(w, r, q.UserID)
	if !ok {
		return
	}
	p, err := loadOrCreate(uid)
	if err != nil {
		internalErr(w, err)
		return
	}
	now := game.ServerNow()
	res := game.SettleIdle(game.IdleInput{Now: now, LastSeen: p.World.LastSeen, Level: p.Core.Level})
	store.ApplyIdleResult(p, res, now, maxHpOf(p))
	if err := disk.Save(uid, p); err != nil {
		internalErr(w, err)
		return
	}
	jsonOut(w, map[string]any{"result": res, "state": p, "stats": playerStats(p)})
}

func transferClass(w http.ResponseWriter, r *http.Request) {
	var q struct{ UserID, To string }
	_ = json.NewDecoder(r.Body).Decode(&q)
	uid, ok := mustUser(w, r, q.UserID)
	if !ok {
		return
	}
	p, err := loadOrCreate(uid)
	if err != nil {
		internalErr(w, err)
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
	if err := disk.Save(uid, p); err != nil {
		internalErr(w, err)
		return
	}
	jsonOut(w, map[string]any{"classId": q.To, "message": "转职成功，装备属性已按新职业系数换算", "state": p, "stats": playerStats(p)})
}

// playerAction：玩家操作的单一权威入口（BD/装备/仓库/副本/家园）。
// 客户端只传意图与参数，服务端校验后落库并返回最新 state。
func playerAction(w http.ResponseWriter, r *http.Request) {
	var q struct {
		UserID   string            `json:"userId"`
		Action   string            `json:"action"`
		Slots    []string          `json:"slots"`    // submit_bd
		Passives []string          `json:"passives"` // save_passives
		SkillID  string            `json:"skillId"`  // unlock_skill
		ItemID   string            `json:"itemId"`   // equip/unequip/deposit/withdraw/storage_decompose
		All      bool              `json:"all"`      // storage_decompose 批量分解全部
		Qty      int               `json:"qty"`      // deposit/withdraw
		Slot     string            `json:"slot"`     // enhance
		DGID     string            `json:"dungeonId"`
		QuestID  string            `json:"questId"`
		HomeLook map[string]string `json:"homeLook"` // update_look
		To       string            `json:"to"`       // class_advance
		ShopItem string            `json:"shopItem"` // shop_exchange：material/exp
		PayWith  string            `json:"payWith"`  // shop_exchange：gold/gem
	}
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	uid, ok := mustUser(w, r, q.UserID)
	if !ok {
		return
	}
	p, err := loadOrCreate(uid)
	if err != nil {
		internalErr(w, err)
		return
	}

	var logs []string
	var extra map[string]any = map[string]any{}
	switch q.Action {
	case "submit_bd":
		slots, e := p.SubmitBD(q.Slots)
		if e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		extra["slots"] = slots
	case "save_passives":
		// v1.9.89 被动装配（最多 2 个，转职后才有池）：服务端权威校验后落库并重算战力
		ps, e := p.SavePassives(q.Passives)
		if e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		extra["passives"] = ps
	case "update_look":
		// 家园换装（服务端权威：只改 appearance，不碰职业/装备/技能）
		look, e := p.UpdateLook(q.HomeLook)
		if e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
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
		if e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		extra["unlocked"] = unlocked
	case "equip":
		logs, err = p.EquipItem(q.ItemID)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
	case "unequip":
		logs, err = p.UnequipItem(q.ItemID)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
	case "enhance":
		// v1.9.20/21：消耗按配置公式 costBase × costGrowth^当前强化等级 递增；
		// v1.9.21 支持按装备实例 ID（itemId）精确定位，避免同槽位多件装备强化错对象。
		enh := game.C().Equipment.Enhance
		goldBase := 1200
		if v, ok := enh["costBase"].(float64); ok {
			goldBase = int(v)
		}
		growth := 1.35
		if v, ok := enh["costGrowth"].(float64); ok {
			growth = v
		}
		matCost := 1 // v1.9.41 由 2 下调
		curLv := 0
		for _, e := range p.Equip {
			if (q.ItemID != "" && e.ID == q.ItemID) || (q.ItemID == "" && e.Slot == q.Slot) {
				curLv = e.Enhance
				break
			}
		}
		goldCost := int(float64(goldBase) * math.Pow(growth, float64(curLv)))
		lvl, e := p.EnhanceEquip(q.ItemID, q.Slot, goldCost, matCost)
		if e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
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
			if v, ok := cfg["goldMult"].(float64); ok {
				goldMult = v
			}
			if v, ok := cfg["matMult"].(float64); ok {
				matMult = v
			}
		}
		gb, mb := decomposeTables()
		// 目标新品质档（当前+1）的基准值决定消耗
		curQ := -1
		for i := range p.Equip {
			if p.Equip[i].ID == q.ItemID {
				curQ = p.Equip[i].Quality
				break
			}
		}
		if curQ < 0 {
			for i := range p.Warehouse {
				if p.Warehouse[i].Kind == "equip" && p.Warehouse[i].ID == q.ItemID && p.Warehouse[i].Equip != nil {
					curQ = p.Warehouse[i].Equip.Quality
					break
				}
			}
		}
		if curQ < 0 {
			http.Error(w, "未找到装备："+q.ItemID, 400)
			return
		}
		newQ := curQ + 1
		if newQ >= len(gb) {
			http.Error(w, "已达最高品质（混沌），无法继续升品", 409)
			return
		}
		goldCost := int(float64(gb[newQ]) * goldMult)
		matCost := int(float64(mb[newQ]) * matMult)
		newQ2, _, e := p.QualityUp(q.ItemID, goldCost, matCost, mults)
		if e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		extra["quality"] = newQ2
		extra["cost"] = map[string]int{"gold": goldCost, "material": matCost}
		extra["gold"] = p.Core.Gold
		extra["material"] = p.Core.Material
	case "use_potion":
		// v1.9.27 使用回血药水：服务端权威校验持有/满血，回血量按配置百分比
		potCfg, _ := game.C().Economy.Drop["potion"].(map[string]any)
		healPct := 0.35
		if m, ok := potCfg[q.ItemID].(map[string]any); ok {
			if v, ok := m["healPct"].(float64); ok && v > 0 && v <= 1 {
				healPct = v
			}
		}
		healed, e := p.UsePotion(q.ItemID, healPct, maxHpOf(p))
		if e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		extra["healed"] = healed
		extra["hp"] = p.Core.HP
		extra["maxHp"] = maxHpOf(p)
	case "deposit":
		if err = p.WarehouseDeposit(q.ItemID, q.Qty); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
	case "withdraw":
		if err = p.WarehouseWithdraw(q.ItemID, q.Qty); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
	case "wh_equip_in": // 背包装备 → 仓库（点击背包装备存入）
		if err = p.WarehouseEquipIn(q.ItemID); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
	case "wh_equip_out": // 仓库装备 → 背包（点击仓库装备取出）
		if err = p.WarehouseEquipOut(q.ItemID); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
	case "storage_decompose": // v1.9.40 分解仓库装备 / v1.9.41 加材料返还 + 批量分解
		enhMult := 0.15
		if cfg, ok := game.C().Economy.Drop["decompose"].(map[string]any); ok {
			if v, ok := cfg["enhanceMult"].(float64); ok {
				enhMult = v
			}
		}
		base := []any{} // 兼容下方 target 品质取值逻辑
		matBase := []any{}
		gb, mb := decomposeTables()
		for _, v := range gb {
			base = append(base, float64(v))
		}
		for _, v := range mb {
			matBase = append(matBase, float64(v))
		}
		store.SetDecomposeConfig(gb, mb, enhMult)
		if q.All { // 批量：分解仓库全部装备
			n, gold, mat := p.WarehouseDecomposeAll()
			if n == 0 {
				http.Error(w, "仓库中没有装备可分解", 400)
				return
			}
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
			if qi < 0 || qi >= len(base) {
				qi = 0
			}
			g, _ := base[qi].(float64)
			m, _ := matBase[qi].(float64)
			goldGain = int(g * (1 + float64(target.Enhance)*enhMult))
			matGain = int(m)
		}
		gGain, mGain, e := p.WarehouseDecompose(q.ItemID, goldGain, matGain)
		if e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		extra["goldGain"] = gGain
		extra["matGain"] = mGain
		extra["gold"] = p.Core.Gold
		extra["material"] = p.Core.Material
	case "dungeon_enter":
		perDay := 0
		var mon string
		var elite bool
		var lvReq int
		for _, d := range game.C().Economy.Dungeons {
			if d.ID == q.DGID {
				perDay = d.PerDay
				mon = d.Mon
				elite = d.Elite
				lvReq = d.Level
				break
			}
		}
		if perDay <= 0 {
			http.Error(w, "副本不存在", 400)
			return
		}
		if p.Core.Level < lvReq {
			http.Error(w, fmt.Sprintf("等级不足：需 Lv.%d", lvReq), 403)
			return
		}
		remain, e := p.DungeonEnter(q.DGID, perDay)
		if e != nil {
			http.Error(w, e.Error(), 409)
			return
		}
		extra["remain"] = remain
		extra["monsterId"] = mon
		extra["elite"] = elite
	case "dungeon_remain":
		perDay := 0
		for _, d := range game.C().Economy.Dungeons {
			if d.ID == q.DGID {
				perDay = d.PerDay
				break
			}
		}
		extra["remain"] = p.DungeonRemain(q.DGID, perDay)
	case "guild_donate":
		if p.Guild == nil {
			http.Error(w, "尚未加入公会", 409)
			return
		}
		if p.Ext == nil {
			p.Ext = map[string]any{}
		}
		today := time.Now().Format("2006-01-02")
		if d, _ := p.Ext["guildDay"].(string); d != today {
			p.Ext["guildDay"] = today
			p.Ext["guildDonated"] = float64(0)
		}
		donated := 0
		if v, ok := p.Ext["guildDonated"].(float64); ok {
			donated = int(v)
		}
		if donated >= 3 {
			http.Error(w, "今日捐献次数已用完（3/3）", 409)
			return
		}
		const cost = 600 // v1.9.41 由 1000 下调
		if p.Core.Gold < cost {
			http.Error(w, fmt.Sprintf("金币不足（需 %d）", cost), 400)
			return
		}
		p.Core.Gold -= cost
		coin := 0
		if v, ok := p.Ext["guildCoin"].(float64); ok {
			coin = int(v)
		}
		// v1.9.56 捐献奖励 ×10：公会币 50→500、经验 60→600（捐献消耗与公会进度不变，
		// 进度是公会共享等级进度，跟着 ×10 会让公会等级一夜暴涨，故保持 +50）
		coin += 500
		p.Ext["guildCoin"] = float64(coin)
		p.Ext["guildDonated"] = float64(donated + 1)
		p.GainExp(600)
		// v1.9.18：公会进度 +50，成员等级/战力快照同步进注册表（服务端权威）
		progress, _, derr := store.Guilds().GuildDonate(p.Guild.ID, p.UserID, p.Core.Level, p.Core.BattlePower)
		if derr != nil {
			http.Error(w, derr.Error(), 409)
			return
		}
		extra["guildCoin"] = coin
		extra["guildDonated"] = donated + 1
		extra["guildProgress"] = progress
	case "guild_buy":
		if p.Guild == nil {
			http.Error(w, "尚未加入公会", 409)
			return
		}
		if p.Ext == nil {
			p.Ext = map[string]any{}
		}
		prices := map[string]int{"gem_pack": 200, "material_pack": 150, "skill_book": 300}
		price, ok := prices[q.ItemID]
		if !ok {
			http.Error(w, "未知商品", 400)
			return
		}
		coin := 0
		if v, ok := p.Ext["guildCoin"].(float64); ok {
			coin = int(v)
		}
		if coin < price {
			http.Error(w, "公会币不足", 400)
			return
		}
		coin -= price
		p.Ext["guildCoin"] = float64(coin)
		switch q.ItemID {
		case "gem_pack":
			p.Core.Gem += 60
		case "material_pack":
			p.Core.Material += 30
		case "skill_book":
			p.Core.Gem += 30
			p.GainExp(300)
		}
		extra["guildCoin"] = coin
	case "quest_claim":
		if p.Ext == nil {
			p.Ext = map[string]any{}
		}
		var rw questReward
		if _, isDaily := dailyQuestDefs[q.QuestID]; isDaily {
			var e error
			rw, e = claimDailyQuest(p, q.QuestID, game.ServerNow())
			if e != nil {
				http.Error(w, e.Error(), 409)
				return
			}
		} else {
			var ok bool
			rw, ok = questRewards[q.QuestID]
			if !ok {
				http.Error(w, "未知任务", 400)
				return
			}
			claimed, _ := p.Ext["quests"].(map[string]any)
			if claimed == nil {
				claimed = map[string]any{}
			}
			if claimed[q.QuestID] == true {
				http.Error(w, "该任务奖励已领取", 409)
				return
			}
			claimed[q.QuestID] = true
			p.Ext["quests"] = claimed
		}
		p.Core.Gold += rw.Gold
		p.Core.Gem += rw.Gem
		p.Core.Material += rw.Mat
		p.GainExp(rw.Exp)
		extra["reward"] = map[string]int{"gold": rw.Gold, "gem": rw.Gem, "exp": rw.Exp, "material": rw.Mat}
	case "shop_exchange":
		// v1.9.53 商店：金币/钻石 → 强化材料 / 经验（比例服务端权威，前端只报商品+货币+数量）
		cost, got, e := p.ShopExchange(q.ShopItem, q.PayWith, q.Qty)
		if e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		extra["cost"] = cost
		extra["got"] = got
		extra["shopItem"] = q.ShopItem
		extra["payWith"] = q.PayWith
	case "shop_sell":
		// v1.9.56 背包回收：材料/药水 → 金币或钻石（单价读 economy.shop.sell，服务端扣减）
		unit, got, e := p.ShopSell(q.ItemID, q.Qty, q.PayWith)
		if e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		extra["unit"] = unit
		extra["got"] = got
		extra["itemId"] = q.ItemID
		extra["payWith"] = q.PayWith
		extra["qty"] = q.Qty
	case "skill_upgrade":
		// v1.9.56 技能升级：每级效果 +8%，消耗钻石随等级递增（服务端权威）
		lv, cost, e := p.SkillUpgrade(q.SkillID)
		if e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		if skill, ok := game.C().SkillByID(q.SkillID); ok {
			extra["skillName"] = skill.Name
		}
		extra["skillId"] = q.SkillID
		extra["level"] = lv
		extra["cost"] = cost
		extra["mult"] = game.SkillEffectMult(lv)
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
	if err := disk.Save(uid, p); err != nil {
		internalErr(w, err)
		return
	}
	extra["logs"] = logs
	extra["state"] = p
	extra["stats"] = playerStats(p) // v1.9.60 省掉前端紧跟的 /api/stats/calc 往返
	jsonOut(w, extra)
}

// 主线任务奖励（服务端白名单：前端只报 questId，金额不可篡改）
type questReward struct{ Gold, Gem, Exp, Mat int }

var questRewards = map[string]questReward{
	"q_kill3":      {800, 30, 300, 0},
	"q_chest1":     {600, 20, 200, 2},
	"q_equip4":     {900, 40, 400, 0},
	"q_enhance1":   {700, 25, 250, 1},
	"q_travel":     {1500, 80, 800, 5},
	"q2_eva_talk":  {300, 10, 100, 0},
	"q2_herb3":     {600, 15, 200, 1},
	"q2_w2_win5":   {1200, 30, 500, 2},
	"q2_w2_chest2": {1000, 25, 400, 3},
	"q2_enhance1":  {900, 20, 300, 2},
	"q2_travel_w3": {2200, 80, 1200, 8},
}

// 每日任务目标与基础奖励。金币/经验按领奖时等级成长，钻石/材料固定。
type dailyQuestDef struct {
	Kind   string
	Goal   int
	Reward questReward
}

var dailyQuestDefs = map[string]dailyQuestDef{
	"d_battle5": {Kind: "battle", Goal: 5, Reward: questReward{Gold: 800, Gem: 10, Exp: 200}},
	"d_chest2":  {Kind: "chest", Goal: 2, Reward: questReward{Gold: 500, Gem: 5, Exp: 100, Mat: 1}},
	"d_herb3":   {Kind: "herb", Goal: 3, Reward: questReward{Gold: 400, Gem: 5, Exp: 100, Mat: 1}},
}

// ensureDailyQuests 以服务端日历日维护当日进度；跨天只替换 dailyQuests，不碰永久主线记录。
func ensureDailyQuests(p *store.PlayerState, now int64) (map[string]any, bool) {
	if p.Ext == nil {
		p.Ext = map[string]any{}
	}
	today := time.Unix(now, 0).Format("2006-01-02")
	daily, _ := p.Ext["dailyQuests"].(map[string]any)
	date, _ := daily["date"].(string)
	if daily == nil || date != today {
		daily = map[string]any{
			"date":     today,
			"progress": map[string]any{},
			"claimed":  map[string]any{},
		}
		p.Ext["dailyQuests"] = daily
		return daily, true
	}
	changed := false
	if progress, _ := daily["progress"].(map[string]any); progress == nil {
		daily["progress"] = map[string]any{}
		changed = true
	}
	if claimed, _ := daily["claimed"].(map[string]any); claimed == nil {
		daily["claimed"] = map[string]any{}
		changed = true
	}
	return daily, changed
}

// advanceDailyQuest 只由服务端权威成功事件调用，不接受客户端直接上报。
func advanceDailyQuest(p *store.PlayerState, kind string, now int64) {
	daily, _ := ensureDailyQuests(p, now)
	progress, _ := daily["progress"].(map[string]any)
	progress[kind] = numAny(progress, kind, 0) + 1
}

func dailyRewardAtLevel(base questReward, level int) questReward {
	if level < 1 {
		level = 1
	}
	multiplier := 1 + float64(level-1)*0.1
	base.Gold = int(math.Floor(float64(base.Gold)*multiplier + 1e-9))
	base.Exp = int(math.Floor(float64(base.Exp)*multiplier + 1e-9))
	return base
}

func claimDailyQuest(p *store.PlayerState, questID string, now int64) (questReward, error) {
	def, ok := dailyQuestDefs[questID]
	if !ok {
		return questReward{}, fmt.Errorf("未知每日任务")
	}
	daily, _ := ensureDailyQuests(p, now)
	claimed, _ := daily["claimed"].(map[string]any)
	if claimed[questID] == true {
		return questReward{}, fmt.Errorf("今日任务奖励已领取")
	}
	progress, _ := daily["progress"].(map[string]any)
	current := int(numAny(progress, def.Kind, 0))
	if current < def.Goal {
		return questReward{}, fmt.Errorf("任务进度不足（%d/%d）", current, def.Goal)
	}
	claimed[questID] = true
	return dailyRewardAtLevel(def.Reward, p.Core.Level), nil
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
	// v1.9.89 必须带被动：+生命百分比的被动要提升血量上限，否则战斗里 CalcStats 的 HP
	// 会大于这里的上限，回血被封顶、面板血量条也会溢出
	return game.CalcStats(game.Avatar{Level: p.Core.Level, ClassID: p.Core.ClassID, Equip: equip,
		Skills: p.Skills.Slots, SkillLv: p.Skills.Levels, Passives: p.Skills.Passives}).HP
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

// index() 静态托管首页：把 index.html 里 app.js / style.css 的 ?v= 自动替换为
// 磁盘文件的 mtime 版本号（60s 内存缓存），改前端文件后无需手改版本号。
var (
	idxMu      sync.Mutex
	idxCached  string
	idxCacheAt time.Time
	reAssetVer = regexp.MustCompile(`\?v=[^"'&\s]+`)
)

func index(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store") // HTML 强制不缓存，确保每次拿到最新脚本引用（v1.9.24 补充）
	if webFile != "" {
		if html := renderIndex(webFile); html != "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte(html))
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

// renderIndex 读取 index.html，把 app.js/style.css 的版本号替换为文件 mtime（60s 缓存）。
func renderIndex(path string) string {
	idxMu.Lock()
	defer idxMu.Unlock()
	if idxCached != "" && time.Since(idxCacheAt) < 60*time.Second {
		return idxCached
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	html := string(b)
	// 取 res/js 与 res/css 下最新 mtime 作为统一版本号：任何前端文件更新都会自动换掉所有 ?v=
	ver := ""
	if webResDir != "" {
		for _, sub := range []string{"js", "css"} {
			entries, e := os.ReadDir(filepath.Join(webResDir, sub))
			if e != nil {
				continue
			}
			for _, ent := range entries {
				if ent.IsDir() {
					continue
				}
				if fi, e2 := ent.Info(); e2 == nil {
					if v := strconv.FormatInt(fi.ModTime().UnixNano(), 10); v > ver {
						ver = v
					}
				}
			}
		}
	}
	if ver != "" {
		html = reAssetVer.ReplaceAllString(html, "?v="+ver)
	}
	idxCached = html
	idxCacheAt = time.Now()
	return html
}

// ---------- 存档备份（每 6 小时，保留最近 7 份） ----------

const backupKeep = 7

func startBackupLoop(dataDir string) {
	go func() {
		backupSaves(dataDir) // 启动先备份一次
		t := time.NewTicker(6 * time.Hour)
		defer t.Stop()
		for range t.C {
			backupSaves(dataDir)
		}
	}()
}

// backupSaves 把 data/saves 复制到 data/backups/<yyyyMMdd-HHmmss>/；失败只记日志。
func backupSaves(dataDir string) {
	src := filepath.Join(dataDir, "saves")
	root := filepath.Join(dataDir, "backups")
	entries, err := os.ReadDir(src)
	if err != nil {
		log.Printf("[backup] 读取存档目录失败: %v", err)
		return
	}
	dst := filepath.Join(root, time.Now().Format("20060102-150405"))
	if err := os.MkdirAll(dst, 0755); err != nil {
		log.Printf("[backup] 创建备份目录失败: %v", err)
		return
	}
	fail := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if err := copyFile(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
			fail++
			log.Printf("[backup] 复制 %s 失败: %v", e.Name(), err)
		}
	}
	log.Printf("[backup] 已备份 %d 个文件到 %s（失败 %d）", len(entries)-fail, dst, fail)
	pruneBackups(root, backupKeep)
}

// copyFile 先写临时文件再 rename，避免备份中出现半截文件。
func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// pruneBackups 只保留最近 keep 份备份目录（按目录名时间戳排序）。
func pruneBackups(root string, keep int) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	dirs := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	if len(dirs) <= keep {
		return
	}
	sortStrings(dirs)
	for _, name := range dirs[:len(dirs)-keep] {
		if err := os.RemoveAll(filepath.Join(root, name)); err != nil {
			log.Printf("[backup] 清理旧备份 %s 失败: %v", name, err)
		}
	}
}

func sortStrings(ss []string) {
	for i := 0; i < len(ss); i++ {
		for j := i + 1; j < len(ss); j++ {
			if ss[j] < ss[i] {
				ss[i], ss[j] = ss[j], ss[i]
			}
		}
	}
}
