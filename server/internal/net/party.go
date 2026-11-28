package net

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"sync"
	"time"

	"zjcs/server/internal/game"
	"zjcs/server/internal/store"
)

// v1.9.85 组队副本：严格状态机 + 身份校验
//
// 安全与边界原则：
//  1. 身份只认 token（authUserID），请求体里的 userId 仅做一致性校验，不一致直接 403；
//     旧实现信任请求体 userId，任何人都能冒充别人建队/踢人/开团。
//  2. 每个操作都先校验「调用者是否真的在这个队伍里、是不是队长、队伍状态是否允许」，
//     非法状态一律拒绝。
//  3. 队伍有明确状态：等待中(Starting=false) / 结算中(Starting=true)。
//     结算中禁止入队、退队、踢人、邀请、再次开始。
//  4. 一个玩家同时只能在一个队伍里；partyOf 索引与 Members 在每次变更后保持一致。
//  5. 只有组队副本（RecPower>0）可以走组队入口，普通副本不能。

const (
	partyMaxMembers = 3
	inviteTTLms     = 60 * 1000
	startingTTLms   = 90 * 1000 // 结算中状态兜底解除，防止卡死
	startCooldownMs = 5000      // 同一队伍两次开团的最小间隔
)

type partyMember struct {
	UserID   string
	Name     string
	ClassID  string
	JoinedAt int64
}

type party struct {
	ID        string
	LeaderID  string
	DGID      string
	Members   []partyMember
	CreatedAt int64
	Starting  bool
	StartAt   int64
	LastStart int64 // 上次开团时间：用于开团冷却，防止并发重复结算
	LastActive int64 // 最近一次队伍活动时间（创建/加入/离开/踢人/邀请/开始/状态查询）
	Run       *game.PartyBattleReport
	RunAt     int64
}

type inviteRec struct {
	PartyID   string
	FromID    string
	FromName  string
	CreatedAt int64
}

var (
	partyMu sync.Mutex
	parties = map[string]*party{}
	partyOf = map[string]string{}
	invites = map[string]*inviteRec{}
)

func nowMs() int64 { return time.Now().UnixMilli() }

func newPartyID() string { return fmt.Sprintf("p_%d", time.Now().UnixNano()) }

var partyJanitorOnce sync.Once

// startPartyJanitor 启动 30s 周期回收器：清理过期邀请、超时结算态、超时战报与离线散队。
// sync.Once 保证只启动一次（在 HandlerWithWeb 中调用）。
func startPartyJanitor() {
	partyJanitorOnce.Do(func() {
		go func() {
			t := time.NewTicker(30 * time.Second)
			defer t.Stop()
			for range t.C {
				partyJanitorTick()
			}
		}()
	})
}

func partyJanitorTick() {
	partyMu.Lock()
	defer partyMu.Unlock()
	pruneInvites()
	now := nowMs()
	online := onlineSet()
	const idleTTL = 10 * 60 * 1000 // 10 分钟
	for _, p := range parties {
		p.settleIfStuck()
		// 战报超过 10 分钟清空，避免旧战报一直回放
		if p.Run != nil && p.RunAt > 0 && now-p.RunAt > idleTTL {
			p.Run = nil
			p.RunAt = 0
		}
		// 所有成员离线且队伍长时间无活动 → 解散
		if now-p.LastActive > idleTTL {
			allOffline := true
			for _, m := range p.Members {
				if online[m.UserID] {
					allOffline = false
					break
				}
			}
			if allOffline {
				p.disband()
			}
		}
	}
}

// mustUser 只认 token；body 里若带了 userId 必须与 token 一致
func mustUser(w http.ResponseWriter, r *http.Request, bodyUserID string) (string, bool) {
	uid, ok := authUserID(r)
	if !ok || uid == "" {
		http.Error(w, "登录状态已失效，请重新登录", 401)
		return "", false
	}
	if bodyUserID != "" && bodyUserID != uid {
		log.Printf("[auth] 越权请求被拒: user=%s body=%s path=%s", uid, bodyUserID, r.URL.Path)
		http.Error(w, "请求身份与登录身份不一致", 403)
		return "", false
	}
	return uid, true
}

func partyDungeon(id string) (game.DungeonDef, bool) {
	for _, d := range game.C().Economy.Dungeons {
		if d.ID == id {
			return d, true
		}
	}
	return game.DungeonDef{}, false
}

// partyDungeonOf 只接受组队副本（RecPower>0）
func partyDungeonOf(id string) (game.DungeonDef, bool) {
	d, ok := partyDungeon(id)
	if !ok || d.RecPower <= 0 {
		return game.DungeonDef{}, false
	}
	if d.PartyMax <= 0 {
		d.PartyMax = partyMaxMembers
	}
	return d, true
}

func onlineSet() map[string]bool {
	out := map[string]bool{}
	for _, p := range OnlinePlayers() {
		if uid, ok := p["userId"].(string); ok && uid != "" {
			out[uid] = true
		}
	}
	return out
}

func (p *party) indexOf(uid string) int {
	for i, m := range p.Members {
		if m.UserID == uid {
			return i
		}
	}
	return -1
}

func (p *party) maxMembers() int {
	if d, ok := partyDungeonOf(p.DGID); ok {
		return d.PartyMax
	}
	return partyMaxMembers
}

func (p *party) settleIfStuck() {
	if p.Starting && nowMs()-p.StartAt > startingTTLms {
		p.Starting = false
	}
}

func (p *party) disband() {
	for _, m := range p.Members {
		if partyOf[m.UserID] == p.ID {
			delete(partyOf, m.UserID)
		}
	}
	delete(parties, p.ID)
}

func (p *party) removeMember(uid string) {
	idx := p.indexOf(uid)
	if idx < 0 {
		return
	}
	p.Members = append(p.Members[:idx], p.Members[idx+1:]...)
	if partyOf[uid] == p.ID {
		delete(partyOf, uid)
	}
	if len(p.Members) == 0 {
		p.disband()
		return
	}
	if p.LeaderID == uid {
		oldest := p.Members[0]
		for _, m := range p.Members[1:] {
			if m.JoinedAt < oldest.JoinedAt {
				oldest = m
			}
		}
		p.LeaderID = oldest.UserID
	}
}

func pruneInvites() {
	now := nowMs()
	for k, v := range invites {
		if now-v.CreatedAt > inviteTTLms {
			delete(invites, k)
		}
	}
}

func partyView(p *party) map[string]any {
	p.settleIfStuck()
	on := onlineSet()
	ms := make([]map[string]any, 0, len(p.Members))
	sumPower := 0
	for _, m := range p.Members {
		level, power, cls := 0, 0, m.ClassID
		if st, err := disk.Load(m.UserID); err == nil && st != nil {
			level = st.Core.Level
			cls = st.Core.ClassID
			power = livePower(st)
		}
		sumPower += power
		ms = append(ms, map[string]any{
			"userId": m.UserID, "name": m.Name, "classId": cls,
			"level": level, "power": power, "online": on[m.UserID],
			"leader": m.UserID == p.LeaderID, "joinedAt": m.JoinedAt,
		})
	}
	avg := 0
	if len(ms) > 0 {
		avg = sumPower / len(ms)
	}
	dg, _ := partyDungeon(p.DGID)
	return map[string]any{
		"id": p.ID, "dgId": p.DGID, "dgName": dg.Name, "recPower": dg.RecPower,
		"leaderId": p.LeaderID, "members": ms, "size": len(ms), "max": p.maxMembers(),
		"avgPower": avg, "createdAt": p.CreatedAt,
		"starting": p.Starting, "hasRun": p.Run != nil,
	}
}

func myParty(uid string) *party {
	if pid, ok := partyOf[uid]; ok {
		if p := parties[pid]; p != nil {
			return p
		}
		delete(partyOf, uid)
	}
	return nil
}

// partyList 大厅：只列等待中（非结算中）的队伍
func partyList(w http.ResponseWriter, r *http.Request) {
	var q struct{ UserID string }
	_ = json.NewDecoder(r.Body).Decode(&q)
	uid, ok := mustUser(w, r, q.UserID)
	if !ok {
		return
	}
	partyMu.Lock()
	defer partyMu.Unlock()
	out := make([]map[string]any, 0, len(parties))
	for _, p := range parties {
		p.settleIfStuck()
		if p.Starting {
			continue
		}
		out = append(out, partyView(p))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i]["createdAt"].(int64) > out[j]["createdAt"].(int64)
	})
	resp := map[string]any{"parties": out}
	if p := myParty(uid); p != nil {
		resp["my"] = partyView(p)
	}
	jsonOut(w, resp)
}

// partyCreate 建队
func partyCreate(w http.ResponseWriter, r *http.Request) {
	var q struct{ UserID, DGID string }
	_ = json.NewDecoder(r.Body).Decode(&q)
	uid, ok := mustUser(w, r, q.UserID)
	if !ok {
		return
	}
	dg, ok := partyDungeonOf(q.DGID)
	if !ok {
		http.Error(w, "该副本不支持组队挑战", 400)
		return
	}
	st, err := loadOrCreate(uid)
	if err != nil {
		http.Error(w, "角色数据读取失败，请稍后重试", 500)
		return
	}
	if st.Core.Level < dg.Level {
		http.Error(w, fmt.Sprintf("等级不足：需 Lv.%d", dg.Level), 403)
		return
	}
	partyMu.Lock()
	defer partyMu.Unlock()
	if p := myParty(uid); p != nil {
		http.Error(w, "你已在队伍中，请先退出", 409)
		return
	}
	np := &party{
		ID: newPartyID(), LeaderID: uid, DGID: dg.ID, CreatedAt: nowMs(), LastActive: nowMs(),
		Members: []partyMember{{UserID: uid, Name: st.Core.Name, ClassID: st.Core.ClassID, JoinedAt: nowMs()}},
	}
	parties[np.ID] = np
	partyOf[uid] = np.ID
	jsonOut(w, map[string]any{"ok": true, "my": partyView(np)})
}

// partyJoin 加入队伍
func partyJoin(w http.ResponseWriter, r *http.Request) {
	var q struct{ UserID, PartyID string }
	_ = json.NewDecoder(r.Body).Decode(&q)
	uid, ok := mustUser(w, r, q.UserID)
	if !ok {
		return
	}
	if q.PartyID == "" {
		http.Error(w, "缺少队伍编号", 400)
		return
	}
	partyMu.Lock()
	defer partyMu.Unlock()
	p := parties[q.PartyID]
	if p == nil {
		http.Error(w, "队伍不存在或已解散", 404)
		return
	}
	p.settleIfStuck()
	if p.Starting {
		http.Error(w, "队伍正在结算中，稍后再试", 409)
		return
	}
	if myParty(uid) != nil {
		http.Error(w, "你已在队伍中，请先退出", 409)
		return
	}
	dg, ok := partyDungeonOf(p.DGID)
	if !ok {
		http.Error(w, "该队伍的目标副本已不可用", 409)
		return
	}
	if len(p.Members) >= p.maxMembers() {
		http.Error(w, "队伍已满", 409)
		return
	}
	st, err := loadOrCreate(uid)
	if err != nil {
		http.Error(w, "角色数据读取失败，请稍后重试", 500)
		return
	}
	if st.Core.Level < dg.Level {
		http.Error(w, fmt.Sprintf("等级不足：需 Lv.%d", dg.Level), 403)
		return
	}
	p.Members = append(p.Members, partyMember{UserID: uid, Name: st.Core.Name, ClassID: st.Core.ClassID, JoinedAt: nowMs()})
	partyOf[uid] = p.ID
	p.LastActive = nowMs()
	delete(invites, uid)
	jsonOut(w, map[string]any{"ok": true, "my": partyView(p)})
}

// partyLeave 退出
func partyLeave(w http.ResponseWriter, r *http.Request) {
	var q struct{ UserID string }
	_ = json.NewDecoder(r.Body).Decode(&q)
	uid, ok := mustUser(w, r, q.UserID)
	if !ok {
		return
	}
	partyMu.Lock()
	defer partyMu.Unlock()
	p := myParty(uid)
	if p == nil {
		jsonOut(w, map[string]any{"ok": true})
		return
	}
	if p.Starting {
		http.Error(w, "队伍正在结算中，无法退出", 409)
		return
	}
	pid := p.ID
	p.removeMember(uid)
	cur, alive := parties[pid]
	if alive && cur != nil {
		cur.LastActive = nowMs()
	}
	jsonOut(w, map[string]any{"ok": true, "disbanded": !alive})
}

// partyKick 踢人（仅队长）
func partyKick(w http.ResponseWriter, r *http.Request) {
	var q struct{ UserID, TargetID string }
	_ = json.NewDecoder(r.Body).Decode(&q)
	uid, ok := mustUser(w, r, q.UserID)
	if !ok {
		return
	}
	if q.TargetID == "" {
		http.Error(w, "缺少目标玩家", 400)
		return
	}
	if q.TargetID == uid {
		http.Error(w, "不能踢自己，请使用退出队伍", 400)
		return
	}
	partyMu.Lock()
	defer partyMu.Unlock()
	p := myParty(uid)
	if p == nil {
		http.Error(w, "你不在任何队伍中", 409)
		return
	}
	if p.LeaderID != uid {
		http.Error(w, "只有队长可以踢人", 403)
		return
	}
	if p.Starting {
		http.Error(w, "队伍正在结算中，无法踢人", 409)
		return
	}
	if p.indexOf(q.TargetID) < 0 {
		http.Error(w, "该玩家不在队伍中", 404)
		return
	}
	p.LastActive = nowMs()
	p.removeMember(q.TargetID)
	jsonOut(w, map[string]any{"ok": true, "my": partyView(p)})
}

// partyInvite 邀请（仅队长）
func partyInvite(w http.ResponseWriter, r *http.Request) {
	var q struct{ UserID, TargetID string }
	_ = json.NewDecoder(r.Body).Decode(&q)
	uid, ok := mustUser(w, r, q.UserID)
	if !ok {
		return
	}
	if q.TargetID == "" {
		http.Error(w, "缺少目标玩家", 400)
		return
	}
	if q.TargetID == uid {
		http.Error(w, "不能邀请自己", 400)
		return
	}
	partyMu.Lock()
	defer partyMu.Unlock()
	p := myParty(uid)
	if p == nil {
		http.Error(w, "你不在任何队伍中", 409)
		return
	}
	if p.LeaderID != uid {
		http.Error(w, "只有队长可以邀请", 403)
		return
	}
	p.settleIfStuck()
	if p.Starting {
		http.Error(w, "队伍正在结算中，稍后再试", 409)
		return
	}
	if len(p.Members) >= p.maxMembers() {
		http.Error(w, "队伍已满", 409)
		return
	}
	if !onlineSet()[q.TargetID] {
		http.Error(w, "该玩家不在线", 409)
		return
	}
	if myParty(q.TargetID) != nil {
		http.Error(w, "该玩家已在队伍中", 409)
		return
	}
	pruneInvites()
	fromName := ""
	for _, m := range p.Members {
		if m.UserID == uid {
			fromName = m.Name
		}
	}
	invites[q.TargetID] = &inviteRec{PartyID: p.ID, FromID: uid, FromName: fromName, CreatedAt: nowMs()}
	p.LastActive = nowMs()
	jsonOut(w, map[string]any{"ok": true})
}

// partyAccept 接受邀请
func partyAccept(w http.ResponseWriter, r *http.Request) {
	var q struct{ UserID string }
	_ = json.NewDecoder(r.Body).Decode(&q)
	uid, ok := mustUser(w, r, q.UserID)
	if !ok {
		return
	}
	partyMu.Lock()
	defer partyMu.Unlock()
	pruneInvites()
	inv := invites[uid]
	if inv == nil {
		http.Error(w, "邀请已过期或不存在", 404)
		return
	}
	p := parties[inv.PartyID]
	if p == nil {
		delete(invites, uid)
		http.Error(w, "队伍已解散", 404)
		return
	}
	p.settleIfStuck()
	if p.Starting {
		http.Error(w, "队伍正在结算中，稍后再试", 409)
		return
	}
	if myParty(uid) != nil {
		http.Error(w, "你已在队伍中，请先退出", 409)
		return
	}
	dg, ok := partyDungeonOf(p.DGID)
	if !ok {
		http.Error(w, "该队伍的目标副本已不可用", 409)
		return
	}
	if len(p.Members) >= p.maxMembers() {
		http.Error(w, "队伍已满", 409)
		return
	}
	st, err := loadOrCreate(uid)
	if err != nil {
		http.Error(w, "角色数据读取失败，请稍后重试", 500)
		return
	}
	if st.Core.Level < dg.Level {
		http.Error(w, fmt.Sprintf("等级不足：需 Lv.%d", dg.Level), 403)
		return
	}
	p.Members = append(p.Members, partyMember{UserID: uid, Name: st.Core.Name, ClassID: st.Core.ClassID, JoinedAt: nowMs()})
	partyOf[uid] = p.ID
	p.LastActive = nowMs()
	delete(invites, uid)
	jsonOut(w, map[string]any{"ok": true, "my": partyView(p)})
}

// partyDecline 拒绝邀请
func partyDecline(w http.ResponseWriter, r *http.Request) {
	var q struct{ UserID string }
	_ = json.NewDecoder(r.Body).Decode(&q)
	uid, ok := mustUser(w, r, q.UserID)
	if !ok {
		return
	}
	partyMu.Lock()
	delete(invites, uid)
	partyMu.Unlock()
	jsonOut(w, map[string]any{"ok": true})
}

// partyStatus 当前队伍状态（含最近战报 / 待处理邀请）
func partyStatus(w http.ResponseWriter, r *http.Request) {
	var q struct{ UserID string }
	_ = json.NewDecoder(r.Body).Decode(&q)
	uid, ok := mustUser(w, r, q.UserID)
	if !ok {
		return
	}
	partyMu.Lock()
	defer partyMu.Unlock()
	pruneInvites()
	inv := invites[uid]
	p := myParty(uid)
	if p == nil {
		resp := map[string]any{"my": nil}
		if inv != nil {
			resp["invite"] = map[string]any{"partyId": inv.PartyID, "fromName": inv.FromName}
		}
		jsonOut(w, resp)
		return
	}
	p.settleIfStuck()
	p.LastActive = nowMs()
	resp := map[string]any{"my": partyView(p)}
	if p.Run != nil {
		resp["run"] = p.Run
		resp["runAt"] = p.RunAt
	}
	jsonOut(w, resp)
}

// partyStart 队长开团：一次性结算三波车轮战并写回各成员存档
func partyStart(w http.ResponseWriter, r *http.Request) {
	var q struct{ UserID string }
	_ = json.NewDecoder(r.Body).Decode(&q)
	uid, ok := mustUser(w, r, q.UserID)
	if !ok {
		return
	}
	partyMu.Lock()
	p := myParty(uid)
	if p == nil {
		partyMu.Unlock()
		http.Error(w, "你不在任何队伍中", 409)
		return
	}
	if p.LeaderID != uid {
		partyMu.Unlock()
		http.Error(w, "只有队长可以开始", 403)
		return
	}
	p.settleIfStuck()
	if p.Starting {
		partyMu.Unlock()
		http.Error(w, "队伍正在结算中，请勿重复开始", 409)
		return
	}
	// 结算极快时 Starting 会在下一次请求到达前复位，这里再加一道冷却，杜绝并发重复结算
	if p.LastStart > 0 && nowMs()-p.LastStart < startCooldownMs {
		partyMu.Unlock()
		http.Error(w, "刚刚已经挑战过，请稍后再来", 429)
		return
	}
	p.LastStart = nowMs()
	p.LastActive = nowMs()
	dg, ok := partyDungeonOf(p.DGID)
	if !ok {
		partyMu.Unlock()
		http.Error(w, "该副本不支持组队挑战", 400)
		return
	}
	memberIDs := make([]string, 0, len(p.Members))
	for _, m := range p.Members {
		memberIDs = append(memberIDs, m.UserID)
	}
	p.Starting = true
	p.StartAt = nowMs()
	partyID := p.ID
	partyMu.Unlock()

	release := func() {
		partyMu.Lock()
		if cur := parties[partyID]; cur != nil {
			cur.Starting = false
		}
		partyMu.Unlock()
	}

	heroes := make([]game.PartyHero, 0, len(memberIDs))
	saves := map[string]*store.PlayerState{}
	for _, mid := range memberIDs {
		st, err := loadOrCreate(mid)
		if err != nil || st == nil {
			release()
			http.Error(w, "有队员的角色数据读取失败，请重试", 500)
			return
		}
		saves[mid] = st
		equip := make([]game.EquipItem, 0, len(st.Equip))
		for _, e := range st.Equip {
			if e.Equipped {
				equip = append(equip, game.EquipItem{ID: e.ID, Slot: e.Slot, Quality: e.Quality, Enhance: e.Enhance, Stats: e.Stats, ClassID: e.ClassID})
			}
		}
		heroes = append(heroes, game.PartyHero{
			UserID: mid, Name: st.Core.Name, ClassID: st.Core.ClassID, Level: st.Core.Level,
			// v1.9.89 补上 SkillLv 与 Passives：此前组队没传技能等级，导致队员技能效果
			// 一直按 1 级结算（单人已经吃 +8%/级，两端不一致）。
			Avatar: game.Avatar{Level: st.Core.Level, ClassID: st.Core.ClassID, Equip: equip,
				SkillLv: st.Skills.Levels, Passives: st.Skills.Passives},
			BD: st.Skills.Slots,
		})
	}
	if len(heroes) == 0 {
		release()
		http.Error(w, "队伍成员数据异常，请重试", 500)
		return
	}

	waves := dg.Waves
	if len(waves) == 0 {
		waves = []game.WaveSpec{{Mon: dg.Mon, Lv: dg.Level, Count: 1, HpMult: dg.BossHpMult, AtkMult: 1}}
	}
	rep := game.ResolvePartyBattle(dg.ID, waves, heroes, 0)

	mat := 0
	if rep.Result == "win" {
		mat = int(3 * rep.PartyMult)
	}
	for _, h := range rep.Heroes {
		st := saves[h.UserID]
		if st == nil {
			continue
		}
		st.Core.HP = h.HPEnd
		if rep.Result == "win" {
			st.Core.Exp += rep.Exp
			st.Core.Gold += rep.Gold
			st.Core.Material += mat
			// v1.9.93 具名材料由战报权威给出（ResolvePartyBattle 内按 seed roll），
			// 每位队员各得一份；单人副本走 /api/battle/fight，由战斗结算的 monster 段覆盖。
			store.AddNamedMaterials(st, rep.Materials)
			for st.Core.Level < 200 && st.Core.Exp >= game.ExpNeeded(st.Core.Level) {
				st.Core.Exp -= game.ExpNeeded(st.Core.Level)
				st.Core.Level++
			}
		}
		_ = disk.Save(h.UserID, st)
	}

	partyMu.Lock()
	if cur := parties[partyID]; cur != nil {
		cur.Run = &rep
		cur.RunAt = nowMs()
		cur.Starting = false
	}
	partyMu.Unlock()

	jsonOut(w, map[string]any{"ok": true, "report": rep, "material": mat, "materials": rep.Materials})
}

// partyDungeonInfo 组队副本入口信息
func partyDungeonInfo(w http.ResponseWriter, r *http.Request) {
	var q struct{ UserID string }
	_ = json.NewDecoder(r.Body).Decode(&q)
	uid, ok := mustUser(w, r, q.UserID)
	if !ok {
		return
	}
	list := []map[string]any{}
	for _, d := range game.C().Economy.Dungeons {
		if d.RecPower <= 0 {
			continue
		}
		pm := d.PartyMax
		if pm <= 0 {
			pm = partyMaxMembers
		}
		list = append(list, map[string]any{
			"id": d.ID, "name": d.Name, "lv": d.Level, "recPower": d.RecPower,
			"partyMax": pm, "drop": d.Drop, "unlimited": d.PerDay == 0,
		})
	}
	myPower := 0
	if st, err := disk.Load(uid); err == nil && st != nil {
		myPower = livePower(st)
	}
	jsonOut(w, map[string]any{"dungeons": list, "myPower": myPower})
}
