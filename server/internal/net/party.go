package net

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"zjcs/server/internal/game"
	"zjcs/server/internal/store"
)

// v1.9.68 组队副本：队伍注册表 + 大厅/邀请/开始接口。
//
// 队伍是短生命周期状态，只放内存（重启即清空），成员属性（战力/等级/在线）
// 每次返回时按存档实时刷新，避免出现"入队后战力不再更新"的问题。

const (
	partyMaxMembers = 3
	inviteTTLms     = 60 * 1000
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
	partyMu   sync.Mutex
	parties   = map[string]*party{}
	partyOf   = map[string]string{} // userID → partyID
	invites   = map[string]*inviteRec{}
)

func nowMs() int64 { return time.Now().UnixMilli() }

func dungeonByID(id string) (game.DungeonDef, bool) {
	for _, d := range game.C().Economy.Dungeons {
		if d.ID == id {
			return d, true
		}
	}
	return game.DungeonDef{}, false
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

// partyView 渲染队伍快照（战力/等级/在线按存档实时取）。
func partyView(p *party) map[string]any {
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
	dg, _ := dungeonByID(p.DGID)
	return map[string]any{
		"id": p.ID, "dgId": p.DGID, "dgName": dg.Name, "recPower": dg.RecPower,
		"leaderId": p.LeaderID, "members": ms, "size": len(ms), "max": dg.PartyMax,
		"avgPower": avg, "createdAt": p.CreatedAt, "hasRun": p.Run != nil,
	}
}

func myParty(uid string) *party {
	if pid, ok := partyOf[uid]; ok {
		return parties[pid]
	}
	return nil
}

func pruneInvites() {
	now := nowMs()
	for k, v := range invites {
		if now-v.CreatedAt > inviteTTLms {
			delete(invites, k)
		}
	}
}

func newPartyID() string {
	return fmt.Sprintf("p_%d", time.Now().UnixNano())
}

// partyList 大厅：所有等待中的队伍（含自己所在队伍）。
func partyList(w http.ResponseWriter, r *http.Request) {
	var q struct{ UserID string }
	_ = json.NewDecoder(r.Body).Decode(&q)
	uid := q.UserID
	if uid == "" {
		uid, _ = authUserID(r)
	}
	partyMu.Lock()
	defer partyMu.Unlock()
	out := make([]map[string]any, 0, len(parties))
	for _, p := range parties {
		if p.Run != nil { // 已开打的队伍不进大厅
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

// partyCreate 建队（建队者即队长）。
func partyCreate(w http.ResponseWriter, r *http.Request) {
	var q struct{ UserID, DGID string }
	_ = json.NewDecoder(r.Body).Decode(&q)
	uid := q.UserID
	if uid == "" {
		uid, _ = authUserID(r)
	}
	if uid == "" {
		http.Error(w, "未授权", 401)
		return
	}
	dg, ok := dungeonByID(q.DGID)
	if !ok {
		http.Error(w, "副本不存在", 400)
		return
	}
	p, err := loadOrCreate(uid)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if p.Core.Level < dg.Level {
		http.Error(w, fmt.Sprintf("等级不足：需 Lv.%d", dg.Level), 403)
		return
	}
	partyMu.Lock()
	defer partyMu.Unlock()
	if old := myParty(uid); old != nil {
		http.Error(w, "你已在队伍中，请先退出", 409)
		return
	}
	np := &party{
		ID: newPartyID(), LeaderID: uid, DGID: q.DGID, CreatedAt: nowMs(),
		Members: []partyMember{{UserID: uid, Name: p.Core.Name, ClassID: p.Core.ClassID, JoinedAt: nowMs()}},
	}
	parties[np.ID] = np
	partyOf[uid] = np.ID
	jsonOut(w, map[string]any{"ok": true, "my": partyView(np)})
}

// partyJoin 加入队伍（无战力硬门槛：允许老带新）。
func partyJoin(w http.ResponseWriter, r *http.Request) {
	var q struct{ UserID, PartyID string }
	_ = json.NewDecoder(r.Body).Decode(&q)
	uid := q.UserID
	if uid == "" {
		uid, _ = authUserID(r)
	}
	partyMu.Lock()
	defer partyMu.Unlock()
	p := parties[q.PartyID]
	if p == nil {
		http.Error(w, "队伍不存在或已解散", 404)
		return
	}
	if myParty(uid) != nil {
		http.Error(w, "你已在队伍中", 409)
		return
	}
	dg, _ := dungeonByID(p.DGID)
	if len(p.Members) >= dg.PartyMax {
		http.Error(w, "队伍已满", 409)
		return
	}
	st, err := loadOrCreate(uid)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if st.Core.Level < dg.Level {
		http.Error(w, fmt.Sprintf("等级不足：需 Lv.%d", dg.Level), 403)
		return
	}
	p.Members = append(p.Members, partyMember{UserID: uid, Name: st.Core.Name, ClassID: st.Core.ClassID, JoinedAt: nowMs()})
	partyOf[uid] = p.ID
	delete(invites, uid)
	jsonOut(w, map[string]any{"ok": true, "my": partyView(p)})
}

// partyLeave 退出（队长退出转给最早加入者；只剩队长一人则解散）。
func partyLeave(w http.ResponseWriter, r *http.Request) {
	var q struct{ UserID string }
	_ = json.NewDecoder(r.Body).Decode(&q)
	uid := q.UserID
	if uid == "" {
		uid, _ = authUserID(r)
	}
	partyMu.Lock()
	defer partyMu.Unlock()
	p := myParty(uid)
	if p == nil {
		jsonOut(w, map[string]any{"ok": true})
		return
	}
	removeMember(p, uid)
	jsonOut(w, map[string]any{"ok": true, "disbanded": parties[p.ID] == nil})
}

// partyKick 队长踢人。
func partyKick(w http.ResponseWriter, r *http.Request) {
	var q struct{ UserID, TargetID string }
	_ = json.NewDecoder(r.Body).Decode(&q)
	uid := q.UserID
	if uid == "" {
		uid, _ = authUserID(r)
	}
	partyMu.Lock()
	defer partyMu.Unlock()
	p := myParty(uid)
	if p == nil || p.LeaderID != uid {
		http.Error(w, "只有队长可以踢人", 403)
		return
	}
	if q.TargetID == uid {
		http.Error(w, "不能踢自己，请使用退出队伍", 400)
		return
	}
	removeMember(p, q.TargetID)
	jsonOut(w, map[string]any{"ok": true, "my": partyView(p)})
}

func removeMember(p *party, uid string) {
	kept := p.Members[:0]
	for _, m := range p.Members {
		if m.UserID != uid {
			kept = append(kept, m)
		}
	}
	p.Members = kept
	delete(partyOf, uid)
	if len(p.Members) == 0 {
		delete(parties, p.ID)
		return
	}
	if p.LeaderID == uid {
		// 转给最早加入的成员
		oldest := p.Members[0]
		for _, m := range p.Members[1:] {
			if m.JoinedAt < oldest.JoinedAt {
				oldest = m
			}
		}
		p.LeaderID = oldest.UserID
	}
}

// partyInvite 邀请在线玩家。
func partyInvite(w http.ResponseWriter, r *http.Request) {
	var q struct{ UserID, TargetID string }
	_ = json.NewDecoder(r.Body).Decode(&q)
	uid := q.UserID
	if uid == "" {
		uid, _ = authUserID(r)
	}
	partyMu.Lock()
	defer partyMu.Unlock()
	p := myParty(uid)
	if p == nil || p.LeaderID != uid {
		http.Error(w, "只有队长可以邀请", 403)
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
	invites[q.TargetID] = &inviteRec{PartyID: p.ID, FromID: uid, FromName: p.Members[0].Name, CreatedAt: nowMs()}
	jsonOut(w, map[string]any{"ok": true})
}

// partyAccept 接受邀请（取最近一条）。
func partyAccept(w http.ResponseWriter, r *http.Request) {
	var q struct{ UserID string }
	_ = json.NewDecoder(r.Body).Decode(&q)
	uid := q.UserID
	if uid == "" {
		uid, _ = authUserID(r)
	}
	partyMu.Lock()
	inv := invites[uid]
	partyMu.Unlock()
	if inv == nil {
		http.Error(w, "没有待处理的邀请", 404)
		return
	}

	partyMu.Lock()
	defer partyMu.Unlock()
	p := parties[inv.PartyID]
	if p == nil {
		delete(invites, uid)
		http.Error(w, "队伍已解散", 404)
		return
	}
	dg, _ := dungeonByID(p.DGID)
	if len(p.Members) >= dg.PartyMax {
		http.Error(w, "队伍已满", 409)
		return
	}
	st, err := loadOrCreate(uid)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	p.Members = append(p.Members, partyMember{UserID: uid, Name: st.Core.Name, ClassID: st.Core.ClassID, JoinedAt: nowMs()})
	partyOf[uid] = p.ID
	delete(invites, uid)
	jsonOut(w, map[string]any{"ok": true, "my": partyView(p)})
}

// partyDecline 拒绝邀请。
func partyDecline(w http.ResponseWriter, r *http.Request) {
	var q struct{ UserID string }
	_ = json.NewDecoder(r.Body).Decode(&q)
	uid := q.UserID
	if uid == "" {
		uid, _ = authUserID(r)
	}
	partyMu.Lock()
	delete(invites, uid)
	partyMu.Unlock()
	jsonOut(w, map[string]any{"ok": true})
}

// partyStatus 拉取队伍当前状态（含最近的副本战报）。前端轮询用。
func partyStatus(w http.ResponseWriter, r *http.Request) {
	var q struct{ UserID string }
	_ = json.NewDecoder(r.Body).Decode(&q)
	uid := q.UserID
	if uid == "" {
		uid, _ = authUserID(r)
	}
	partyMu.Lock()
	defer partyMu.Unlock()
	inv := invites[uid]
	pruneInvites()
	p := myParty(uid)
	if p == nil {
		resp := map[string]any{"my": nil}
		if inv != nil {
			resp["invite"] = map[string]any{"partyId": inv.PartyID, "fromName": inv.FromName}
		}
		jsonOut(w, resp)
		return
	}
	resp := map[string]any{"my": partyView(p)}
	if p.Run != nil {
		resp["run"] = p.Run
		resp["runAt"] = p.RunAt
	}
	jsonOut(w, resp)
}

// partyStart 队长发起副本：服务端一次性结算三人的战斗，写回各自存档。
func partyStart(w http.ResponseWriter, r *http.Request) {
	var q struct{ UserID string }
	_ = json.NewDecoder(r.Body).Decode(&q)
	uid := q.UserID
	if uid == "" {
		uid, _ = authUserID(r)
	}
	partyMu.Lock()
	p := myParty(uid)
	if p == nil || p.LeaderID != uid {
		partyMu.Unlock()
		http.Error(w, "只有队长可以开始", 403)
		return
	}
	dg, ok := dungeonByID(p.DGID)
	if !ok {
		partyMu.Unlock()
		http.Error(w, "副本不存在", 400)
		return
	}
	memberIDs := make([]string, 0, len(p.Members))
	for _, m := range p.Members {
		memberIDs = append(memberIDs, m.UserID)
	}
	partyMu.Unlock()

	// 构造参战成员（各取自己存档的当前血量与已穿戴装备）
	heroes := make([]game.PartyHero, 0, len(memberIDs))
	saves := map[string]*store.PlayerState{}
	for _, mid := range memberIDs {
		st, err := loadOrCreate(mid)
		if err != nil || st == nil {
			continue
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
			Avatar: game.Avatar{Level: st.Core.Level, ClassID: st.Core.ClassID, Equip: equip},
			BD:     st.Skills.Slots,
		})
	}
	if len(heroes) == 0 {
		http.Error(w, "队伍成员数据异常，请重试", 500)
		return
	}
	// v1.9.77 三波车轮战：按副本配置逐波推进；未配置 waves 时退化为单波
	waves := dg.Waves
	if len(waves) == 0 {
		waves = []game.WaveSpec{{Mon: dg.Mon, Lv: dg.Level, Count: 1, HpMult: dg.BossHpMult, AtkMult: 1}}
	}
	rep := game.ResolvePartyBattle(dg.ID, waves, heroes, 0)

	// 写回各自存档：血量保留、发奖（胜场才发）
	// v1.9.77 车轮战：三波全通才有奖励，中途团灭为 0（与 rep.Exp/Gold 一致）
	mat := 0
	if rep.Result == "win" {
		mat = int(3 * rep.PartyMult)
	}
	for i, h := range rep.Heroes {
		st := saves[h.UserID]
		if st == nil {
			continue
		}
		st.Core.HP = h.HPEnd
		if rep.Result == "win" {
			st.Core.Exp += rep.Exp
			st.Core.Gold += rep.Gold
			st.Core.Material += mat
			for st.Core.Level < 200 && st.Core.Exp >= game.ExpNeeded(st.Core.Level) {
				st.Core.Exp -= game.ExpNeeded(st.Core.Level)
				st.Core.Level++
			}
		}
		_ = i
		_ = disk.Save(h.UserID, st)
	}

	partyMu.Lock()
	p.Run = &rep
	p.RunAt = nowMs()
	partyMu.Unlock()

	jsonOut(w, map[string]any{"ok": true, "report": rep, "material": mat})
}

// partyDungeonInfo 副本入口信息：推荐战力 + 我的战力 + 今日次数。
func partyDungeonInfo(w http.ResponseWriter, r *http.Request) {
	var q struct{ UserID string }
	_ = json.NewDecoder(r.Body).Decode(&q)
	uid := q.UserID
	if uid == "" {
		uid, _ = authUserID(r)
	}
	list := []map[string]any{}
	for _, d := range game.C().Economy.Dungeons {
		if d.RecPower <= 0 { // 只返回新式组队副本
			continue
		}
		list = append(list, map[string]any{
			"id": d.ID, "name": d.Name, "lv": d.Level, "recPower": d.RecPower,
			"partyMax": d.PartyMax, "drop": d.Drop, "unlimited": d.PerDay == 0,
		})
	}
	myPower := 0
	if st, err := disk.Load(uid); err == nil && st != nil {
		myPower = livePower(st)
	}
	jsonOut(w, map[string]any{"dungeons": list, "myPower": myPower})
}
