// Nakama Go 运行时模块：把 internal/game 的权威逻辑注册为 RPC。
// 编译为 .so 放入 Nakama --runtime.path，启动时由 Nakama 加载。
//
// 存档闭环：所有 RPC 从 session 取 userId（客户端不可伪造），
// 读写 Nakama Storage（collection=game, key=state:<userId>），
// 战斗/离线结算后再把奖励合并落库，客户端只拿回只读结果。
//
// 编译（须与 Nakama 二进制同版本 Go，官方镜像用 Go 1.21.x）：
//   go build -tags nakama -buildmode=plugin -o zjcs.so .
//go:build nakama

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"

	"github.com/heroiclabs/nakama-common/runtime"
	"zjcs/server/internal/game"
	"zjcs/server/internal/store"
)

const (
	collection = "game"
	stateKey   = "state"
	permRead   = 1 // 仅拥有者可读
	permWrite  = 0 // 仅服务端可写
)

func InitModule(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, initializer runtime.Initializer) error {
	cfgDir := getenv("ZJCS_CONFIG_DIR", "/nakama/data/configs")
	if err := game.Init(cfgDir); err != nil {
		logger.Error("配置加载失败: %v", err)
		return err
	}
	type rpcFn = func(context.Context, runtime.Logger, *sql.DB, runtime.NakamaModule, string) (string, error)
	rpc := map[string]rpcFn{
		"get_config":       rpcGetConfig,
		"character_create": rpcCharacterCreate,
		"state_load":       rpcStateLoad,
		"battle_resolve":   rpcBattleResolve,
		"idle_settle":      rpcIdleSettle,
		"transfer_class":   rpcTransferClass,
	}
	for name, fn := range rpc {
		if err := initializer.RegisterRpc(name, fn); err != nil {
			return fmt.Errorf("注册 RPC %s: %w", name, err)
		}
	}
	// 大世界多人匹配：按 worldId 分线，服务端权威位置同步
	if err := initializer.RegisterMatch("world", func(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule) (runtime.Match, error) {
		return &WorldMatch{}, nil
	}); err != nil {
		return fmt.Errorf("注册 Match world: %w", err)
	}
	if err := initializer.RegisterRpc("join_world", rpcJoinWorld); err != nil {
		return fmt.Errorf("注册 RPC join_world: %w", err)
	}
	logger.Info("ZJCS 权威模块已加载 config=%s RPC数=%d Match=world", cfgDir, len(rpc)+1)
	return nil
}

// ---------- 存档读写辅助 ----------

// keyOf 每个用户的存档 key 独立，便于按用户隔离与导出。
func keyOf(uid string) string { return stateKey + ":" + uid }

// loadState 读存档；不存在则按配置创一个初始角色并落库。
func loadState(ctx context.Context, nk runtime.NakamaModule, logger runtime.Logger, uid string) (*store.PlayerState, error) {
	objs, err := nk.StorageRead(ctx, []*runtime.StorageRead{{Collection: collection, Key: keyOf(uid), UserID: uid}})
	if err != nil {
		return nil, err
	}
	if len(objs) > 0 && objs[0].Value != "" {
		var p store.PlayerState
		if err := json.Unmarshal([]byte(objs[0].Value), &p); err != nil {
			return nil, fmt.Errorf("存档解析失败: %w", err)
		}
		return &p, nil
	}
	p := store.NewPlayer(uid, "", "")
	if err := saveState(ctx, nk, logger, uid, p); err != nil {
		return nil, err
	}
	return p, nil
}

func saveState(ctx context.Context, nk runtime.NakamaModule, logger runtime.Logger, uid string, p *store.PlayerState) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = nk.StorageWrite(ctx, []*runtime.StorageWrite{{
		Collection: collection, Key: keyOf(uid), UserID: uid,
		Value: string(b), PermissionRead: permRead, PermissionWrite: permWrite,
	}})
	return err
}

// uidFrom 从 session 上下文取用户 ID（防客户端伪造 userId）。
func uidFrom(ctx context.Context) string {
	if v, ok := ctx.Value(runtime.RUNTIME_CTX_USER_ID).(string); ok {
		return v
	}
	return ""
}

// ---------- RPC 实现 ----------

func rpcGetConfig(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, payload string) (string, error) {
	c := game.C()
	b, _ := json.Marshal(map[string]any{
		"classes": c.Classes, "skills": c.Skills, "equipment": c.Equipment,
		"monsters": c.Monsters, "economy": c.Economy, "worlds": c.Worlds,
	})
	return string(b), nil
}

func rpcCharacterCreate(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, payload string) (string, error) {
	uid := uidFrom(ctx)
	if uid == "" {
		return "", fmt.Errorf("未登录")
	}
	var q struct {
		Name    string `json:"name"`
		ClassID string `json:"classId"`
	}
	_ = json.Unmarshal([]byte(payload), &q)
	p := store.NewPlayer(uid, q.Name, q.ClassID)
	if err := saveState(ctx, nk, logger, uid, p); err != nil {
		return "", err
	}
	b, _ := json.Marshal(p)
	return string(b), nil
}

func rpcStateLoad(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, payload string) (string, error) {
	uid := uidFrom(ctx)
	if uid == "" {
		return "", fmt.Errorf("未登录")
	}
	p, err := loadState(ctx, nk, logger, uid)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(p)
	return string(b), nil
}

func rpcBattleResolve(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, payload string) (string, error) {
	uid := uidFrom(ctx)
	if uid == "" {
		return "", fmt.Errorf("未登录")
	}
	var req game.BattleRequest
	if err := json.Unmarshal([]byte(payload), &req); err != nil {
		return "", err
	}
	// 角色属性以服务端存档为准，忽略客户端上传的 avatar（防伪造等级/装备）
	p, err := loadState(ctx, nk, logger, uid)
	if err != nil {
		return "", err
	}
	equip := make([]game.EquipItem, 0, len(p.Equip))
	for _, e := range p.Equip {
		if e.Equipped {
			equip = append(equip, game.EquipItem{ID: e.ID, Slot: e.Slot, Quality: e.Quality, Enhance: e.Enhance, Stats: e.Stats})
		}
	}
	req.UserID = uid
	req.Avatar = game.Avatar{Level: p.Core.Level, ClassID: p.Core.ClassID, Equip: equip}

	rep := game.ResolveBattle(req)
	store.ApplyBattleReport(p, rep)
	if err := saveState(ctx, nk, logger, uid, p); err != nil {
		return "", err
	}
	b, _ := json.Marshal(rep)
	return string(b), nil
}

func rpcIdleSettle(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, payload string) (string, error) {
	uid := uidFrom(ctx)
	if uid == "" {
		return "", fmt.Errorf("未登录")
	}
	p, err := loadState(ctx, nk, logger, uid)
	if err != nil {
		return "", err
	}
	now := game.ServerNow() // 唯一可信时间源，忽略客户端时间戳
	r := game.SettleIdle(game.IdleInput{Now: now, LastSeen: p.World.LastSeen, Level: p.Core.Level})
	store.ApplyIdleResult(p, r, now, 0)
	if err := saveState(ctx, nk, logger, uid, p); err != nil {
		return "", err
	}
	b, _ := json.Marshal(r)
	return string(b), nil
}

func rpcTransferClass(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, payload string) (string, error) {
	uid := uidFrom(ctx)
	if uid == "" {
		return "", fmt.Errorf("未登录")
	}
	var q struct {
		To string `json:"to"`
	}
	if err := json.Unmarshal([]byte(payload), &q); err != nil {
		return "", err
	}
	p, err := loadState(ctx, nk, logger, uid)
	if err != nil {
		return "", err
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
	if err := saveState(ctx, nk, logger, uid, p); err != nil {
		return "", err
	}
	b, _ := json.Marshal(map[string]any{"classId": q.To, "message": "转职成功，装备属性已按新职业系数换算"})
	return string(b), nil
}

// rpcJoinWorld：按 worldId 找现有线或新建一条，返回 matchId 供客户端 socket.matchJoin。
// 用 MatchLabel 持久保存 worldId，避免玩家分散到不同线。
func rpcJoinWorld(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, payload string) (string, error) {
	uid := uidFrom(ctx)
	if uid == "" {
		return "", fmt.Errorf("未登录")
	}
	var q struct {
		WorldID string `json:"worldId"`
	}
	if err := json.Unmarshal([]byte(payload), &q); err != nil {
		return "", err
	}
	wid := q.WorldID
	if wid == "" {
		wid = "W1"
	}
	// 查已有线（label 匹配 worldId，最多取 1 条未满的）
	label := `{"worldId":"` + wid + `"}`
	matches, err := nk.MatchList(ctx, 100, true, "", nil, nil, "")
	if err != nil {
		return "", err
	}
	for _, m := range matches {
		mlabel := ""
		if m.Label != nil {
			mlabel = m.Label.Value
		}
		if mlabel == label && int(m.Size) < maxPlayers {
			b, _ := json.Marshal(map[string]string{"matchId": m.MatchId, "worldId": wid})
			return string(b), nil
		}
	}
	// 没有则新建
	matchID, err := nk.MatchCreate(ctx, "world", map[string]interface{}{"worldId": wid})
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(map[string]string{"matchId": matchID, "worldId": wid})
	return string(b), nil
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
