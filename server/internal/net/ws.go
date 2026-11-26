// Package net · 同端口 WebSocket 实时多人（Nakama 协议同构，零外部依赖）。
// 最小 RFC6455 实现：HTTP Upgrade 握手 + 文本帧收发；服务端权威位置裁决 + 10Hz 节流广播。
// 协议复用第十章：match_join、player_move(type/seq/v)、heartbeat、state_snapshot。
package net

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"zjcs/server/internal/game"
)

const wsMagic = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// WsClient 一个在线玩家连接。
type WsClient struct {
	id       string
	worldID  string
	channel  int    // 分线号：同 world 同 channel 的玩家互相可见，满载自动开新线
	userID   string
	name     string
	classID  string
	x, y     int
	seq      int64
	conn     net.Conn
	writeMu  sync.Mutex
	lastSeen int64
}

// channelCapacity 每线容量（交付包：40人/线），从 economy.json 读取。
func channelCapacity() int {
	n := int(numAny(game.C().Economy.Multiplayer, "channelCapacity", 40))
	if n <= 0 { n = 40 }
	return n
}

func (c *WsClient) send(v any) {
	b, _ := json.Marshal(v)
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	writeWSFrame(c.conn, b)
}

// Hub 全部在线连接 + 10Hz 广播循环。
type Hub struct {
	mu      sync.RWMutex
	clients map[*WsClient]bool
}

var hub = &Hub{clients: map[*WsClient]bool{}}
var hubOnce sync.Once

func startHub() {
	hubOnce.Do(func() { go hub.loop() })
}

func (h *Hub) loop() {
	tick := time.NewTicker(100 * time.Millisecond) // 10Hz
	defer tick.Stop()
	for range tick.C {
		now := time.Now().UnixMilli()
		h.mu.Lock()
		// 踢掉 15s 无心跳的连接
		for c := range h.clients {
			if now-c.lastSeen > 15000 { c.conn.Close(); delete(h.clients, c) }
		}
		// 按世界+分线分组广播快照（同线可见，跨线不可见）
		type groupKey struct{ world string; ch int }
		groups := map[groupKey][]map[string]any{}
		for c := range h.clients {
			k := groupKey{c.worldID, c.channel}
			groups[k] = append(groups[k], map[string]any{
				"id": c.id, "userId": c.userID, "name": c.name, "classId": c.classID,
				"x": c.x, "y": c.y, "seq": c.seq,
			})
		}
		h.mu.Unlock()
		for k, players := range groups {
			snap := map[string]any{"type": "state_snapshot", "worldId": k.world, "channel": k.ch, "players": players, "ts": now}
			h.mu.RLock()
			for c := range h.clients {
				if c.worldID == k.world && c.channel == k.ch { c.send(snap) }
			}
			h.mu.RUnlock()
		}
	}
}

// OnlinePlayers 当前在线玩家列表（v1.9.42 在线玩家面板）：按 lastSeen 去重（同一 userId 多连接取最新）。
func OnlinePlayers() []map[string]any {
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	type key struct{ uid string }
	seen := map[key]*WsClient{}
	now := time.Now().UnixMilli()
	for c := range hub.clients {
		if now-c.lastSeen > 15000 { continue }
		k := key{c.userID}
		if old, ok := seen[k]; ok && c.lastSeen <= old.lastSeen { continue }
		seen[k] = c
	}
	out := make([]map[string]any, 0, len(seen))
	for _, c := range seen {
		out = append(out, map[string]any{
			"userId": c.userID, "name": c.name, "classId": c.classID,
			"worldId": c.worldID, "channel": c.channel, "x": c.x, "y": c.y,
		})
	}
	return out
}

// wsHandler HTTP Upgrade → WebSocket 连接。
func wsHandler(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		http.Error(w, "需要 WebSocket 升级", 400)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok { http.Error(w, "不支持 Hijack", 500); return }
	conn, buf, err := hj.Hijack()
	if err != nil { return }
	accept := sha1.Sum([]byte(key + wsMagic))
	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + base64.StdEncoding.EncodeToString(accept[:]) + "\r\n\r\n"
	buf.WriteString(resp); buf.Flush()
	startHub()

	// token 从 query 取
	uid, _ := authUserID(r)
	c := &WsClient{id: r.URL.Query().Get("clientId"), userID: uid, worldID: "W1", name: "玩家", conn: conn, lastSeen: time.Now().UnixMilli()}
	if c.id == "" { c.id = uid }
	hub.mu.Lock(); hub.clients[c] = true; hub.mu.Unlock()

	br := bufio.NewReader(conn)
	for {
		msg, err := readWSFrame(br)
		if err != nil { break }
		var env struct {
			Type    string `json:"type"`
			WorldID string `json:"worldId"`
			Channel int    `json:"channel"` // 重连时可带旧线号，未满则复用
			Name    string `json:"name"`
			ClassID string `json:"classId"`
			X       int    `json:"x"`
			Y       int    `json:"y"`
			Seq     int64  `json:"seq"`
			V       any    `json:"v"`
		}
		if json.Unmarshal(msg, &env) != nil { continue }
		c.lastSeen = time.Now().UnixMilli()
		switch env.Type {
		case "match_join":
			hub.mu.Lock()
			c.worldID = env.WorldID
			if env.Name != "" { c.name = env.Name }
			if env.ClassID != "" { c.classID = env.ClassID }
			// 分线分配：找该世界未满的线（重连优先复用旧线），否则开新线
			capN := channelCapacity()
			counts := map[int]int{}
			for other := range hub.clients {
				if other.worldID == c.worldID && other != c { counts[other.channel]++ }
			}
			chosen := env.Channel
			if chosen <= 0 { chosen = c.channel }
			if chosen < 0 || counts[chosen] >= capN {
				chosen = 0
				for counts[chosen] >= capN { chosen++ }
			}
			c.channel = chosen
			hub.mu.Unlock()
			c.send(map[string]any{"type": "match_joined", "worldId": c.worldID, "channel": c.channel, "clientId": c.id})
		case "player_move":
			// 服务端权威：限速（防瞬移）——单帧位移上限 160px，超出则 clamp
			hub.mu.Lock()
			dx, dy := env.X-c.x, env.Y-c.y
			if dx > 160 { dx = 160 }; if dx < -160 { dx = -160 }
			if dy > 160 { dy = 160 }; if dy < -160 { dy = -160 }
			c.x += dx; c.y += dy
			if env.Seq > c.seq { c.seq = env.Seq }
			hub.mu.Unlock()
		case "heartbeat":
			c.send(map[string]any{"type": "heartbeat_ack", "ts": time.Now().UnixMilli()})
		}
	}
	hub.mu.Lock(); delete(hub.clients, c); hub.mu.Unlock()
	conn.Close()
}

// ---------- 最小 RFC6455 帧处理 ----------

func writeWSFrame(conn net.Conn, payload []byte) {
	mask := []byte{0x12, 0x34, 0x56, 0x78} // 服务端→客户端通常不 mask，但保留兼容
	_ = mask
	n := len(payload)
	header := []byte{0x81}
	switch {
	case n < 126:
		header = append(header, byte(0x80|n))
	case n < 65536:
		header = append(header, 0x80|126, byte(n>>8), byte(n))
	default:
		header = append(header, 0x80|127)
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(n))
		header = append(header, b[:]...)
	}
	// 服务端帧不 mask（RFC 规定），FIN+text
	header[1] &= 0x7F
	conn.Write(header)
	conn.Write(payload)
}

func readWSFrame(r *bufio.Reader) ([]byte, error) {
	var h [2]byte
	if _, err := io.ReadFull(r, h[:]); err != nil { return nil, err }
	masked := h[1]&0x80 != 0
	n := int(h[1] & 0x7F)
	switch n {
	case 126:
		var b [2]byte
		io.ReadFull(r, b[:]); n = int(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		io.ReadFull(r, b[:]); n = int(binary.BigEndian.Uint64(b[:]))
	}
	var mask [4]byte
	if masked { io.ReadFull(r, mask[:]) }
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil { return nil, err }
	if masked {
		for i := range payload { payload[i] ^= mask[i%4] }
	}
	return payload, nil
}
