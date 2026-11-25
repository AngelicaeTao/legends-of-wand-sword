//go:build nakama

// 大世界多人匹配处理器：同一 worldId 的玩家进同一条线，服务端以 10Hz 广播位置快照。
// 客户端只上报意图（move），坐标合法性由服务端校验；其他玩家位置全部来自服务端，防瞬移/伪造。
//
// 协议（opCode）：
//
//	1 = move   C→S {"x":..,"y":..,"ts":..,"action":"idle|walk|battle"}
//	2 = snapshot S→C {"players":[{playerId,x,y,action,ts}],"tick":..}
//	3 = join    S→C {"playerId":..}   新玩家加入
//	4 = leave   S→C {"playerId":..}   玩家离开
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/heroiclabs/nakama-common/runtime"
)

const (
	opMove     int64 = 1
	opSnapshot int64 = 2
	opJoin     int64 = 3
	opLeave    int64 = 4

	tickRate       = 10    // 10Hz 广播
	maxSpeed       = 600.0 // 坐标单位/秒，超过视为瞬移，丢弃
	maxPlayers     = 40    // 单线人数上限
	snapshotPeriod = time.Second / tickRate
)

// WorldMatchState 是单条线的权威状态。
type WorldMatchState struct {
	worldID string
	players map[string]*PlayerPresence // sessionId -> 玩家
	tick    int64
	label   string
}

// PlayerPresence 是服务端保存的单个玩家状态（权威坐标）。
type PlayerPresence struct {
	PlayerID  string  `json:"playerId"`
	SessionID string  `json:"-"`
	Username  string  `json:"-"`
	X         float64 `json:"x"`
	Y         float64 `json:"y"`
	Action    string  `json:"action"`
	Ts        int64   `json:"ts"`
	lastMove  time.Time
}

// MoveInput 客户端上报的移动意图（不信任，需服务端校验）。
type MoveInput struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Ts     int64   `json:"ts"` // 客户端时间戳，仅用于排序参考，不作权威
	Action string  `json:"action"`
}

// Snapshot 服务端下发的权威快照。
type Snapshot struct {
	Players []*PlayerPresence `json:"players"`
	Tick    int64             `json:"tick"`
	WorldID string            `json:"worldId"`
}

type WorldMatch struct{}

// speedOK 纯函数：判定一次位移是否在速度上限内（+1 容差应对浮点与抖动）。
// dt<=0 时按一个 tick 周期兜底，避免除零/过快。抽成纯函数便于单测防瞬移逻辑。
func speedOK(oldX, oldY, newX, newY float64, dt float64) bool {
	if dt <= 0 {
		dt = 1.0 / float64(tickRate)
	}
	dist := math.Hypot(newX-oldX, newY-oldY)
	return dist <= maxSpeed*dt+1
}

// MatchInit：从 params 读取 worldId（由 matchmaker 或 RPC 传入），默认 W1。
func (m *WorldMatch) MatchInit(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule,
	params map[string]interface{}) (interface{}, int, string) {
	wid := "W1"
	if v, ok := params["worldId"].(string); ok && v != "" {
		wid = v
	}
	labelMap := map[string]interface{}{"worldId": wid}
	label, _ := json.Marshal(labelMap)
	s := &WorldMatchState{
		worldID: wid,
		players: make(map[string]*PlayerPresence),
		label:   string(label),
	}
	logger.Info("WorldMatch 创建 world=%s tickRate=%d", wid, tickRate)
	return s, tickRate, string(label)
}

// MatchJoinAttempt：限制单线人数。
func (m *WorldMatch) MatchJoinAttempt(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule,
	dispatcher runtime.MatchDispatcher, tick int64, state interface{}, presence runtime.Presence,
	metadata map[string]string) (interface{}, bool, string) {
	s := state.(*WorldMatchState)
	if len(s.players) >= maxPlayers {
		return state, false, "世界线已满"
	}
	return state, true, ""
}

// MatchJoin：登记玩家并广播 join。
func (m *WorldMatch) MatchJoin(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule,
	dispatcher runtime.MatchDispatcher, tick int64, state interface{}, presences []runtime.Presence) interface{} {
	s := state.(*WorldMatchState)
	for _, p := range presences {
		s.players[p.GetSessionId()] = &PlayerPresence{
			PlayerID: p.GetUserId(), SessionID: p.GetSessionId(), Username: p.GetUsername(),
			X: 1920, Y: 1080, Action: "idle", Ts: time.Now().UnixMilli(), lastMove: time.Now(),
		}
		b, _ := json.Marshal(map[string]string{"playerId": p.GetUserId()})
		dispatcher.BroadcastMessage(opJoin, b, nil, nil, true)
		logger.Info("WorldMatch 玩家加入 world=%s user=%s", s.worldID, p.GetUserId())
	}
	return s
}

// MatchLeave：移除玩家并广播 leave。
func (m *WorldMatch) MatchLeave(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule,
	dispatcher runtime.MatchDispatcher, tick int64, state interface{}, presences []runtime.Presence) interface{} {
	s := state.(*WorldMatchState)
	for _, p := range presences {
		if _, ok := s.players[p.GetSessionId()]; ok {
			delete(s.players, p.GetSessionId())
			b, _ := json.Marshal(map[string]string{"playerId": p.GetUserId()})
			dispatcher.BroadcastMessage(opLeave, b, nil, nil, true)
		}
	}
	return s
}

// MatchLoop：处理 move 消息 + 定期广播快照。
func (m *WorldMatch) MatchLoop(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule,
	dispatcher runtime.MatchDispatcher, tick int64, state interface{}, messages []runtime.MatchData) interface{} {
	s := state.(*WorldMatchState)
	now := time.Now()

	// 1) 处理客户端上报
	for _, msg := range messages {
		if msg.GetOpCode() != opMove {
			continue
		}
		p := s.players[msg.GetSessionId()]
		if p == nil {
			continue
		}
		var in MoveInput
		if err := json.Unmarshal(msg.GetData(), &in); err != nil {
			continue
		}
		// 服务端限速：位移/时间 超过 maxSpeed 视为瞬移，忽略本次
		dt := now.Sub(p.lastMove).Seconds()
		if !speedOK(p.X, p.Y, in.X, in.Y, dt) {
			continue // 丢弃异常位移
		}
		p.X, p.Y = in.X, in.Y
		p.Ts = now.UnixMilli()
		if in.Action == "idle" || in.Action == "walk" || in.Action == "battle" {
			p.Action = in.Action
		}
		p.lastMove = now
	}

	// 2) 每 tick 广播权威快照（MatchLoop 按 tickRate=10 调用，每帧即 100ms）
	snap := Snapshot{Players: make([]*PlayerPresence, 0, len(s.players)), Tick: tick, WorldID: s.worldID}
	for _, p := range s.players {
		snap.Players = append(snap.Players, p)
	}
	b, _ := json.Marshal(snap)
	_ = dispatcher.BroadcastMessage(opSnapshot, b, nil, nil, false)
	s.tick = tick
	return s
}

// MatchTerminate：优雅关闭，清空状态。
func (m *WorldMatch) MatchTerminate(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule,
	dispatcher runtime.MatchDispatcher, tick int64, state interface{}, graceSeconds int) interface{} {
	s := state.(*WorldMatchState)
	s.players = make(map[string]*PlayerPresence)
	return s
}

// MatchSignal：预留动态改线/查询（如切换 worldId）。
func (m *WorldMatch) MatchSignal(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule,
	dispatcher runtime.MatchDispatcher, tick int64, state interface{}, data string) (interface{}, string) {
	return state, fmt.Sprintf(`{"worldId":"%s","players":%d}`, state.(*WorldMatchState).worldID, len(state.(*WorldMatchState).players))
}
