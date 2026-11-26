//go:build nakama

package main

import (
	"encoding/json"
	"testing"
)

// 防瞬移：正常移动放行，瞬移丢弃。
func TestSpeedOK(t *testing.T) {
	cases := []struct {
		name string
		ox, oy, nx, ny, dt float64
		want bool
	}{
		{"正常移动 50单位/0.1s", 1920, 1080, 1970, 1080, 0.1, true},
		{"瞬移 5000单位/0.1s", 1920, 1080, 6920, 1080, 0.1, false},
		{"原地不动", 100, 100, 100, 100, 0.1, true},
		{"dt<=0兜底按1tick", 0, 0, 50, 0, 0, true},   // 50 <= 600*0.1+1=61
		{"dt<=0超上限", 0, 0, 200, 0, -1, false},      // 200 > 61
		{"斜向正常", 0, 0, 30, 30, 0.1, true},         // 42.4 <= 61
	}
	for _, c := range cases {
		got := speedOK(c.ox, c.oy, c.nx, c.ny, c.dt)
		if got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

// 快照可序列化且含全部字段。
func TestSnapshotJSON(t *testing.T) {
	s := Snapshot{
		Players: []*PlayerPresence{{PlayerID: "u1", X: 1920, Y: 1080, Action: "walk", Ts: 123}},
		Tick: 42, WorldID: "W1",
	}
	b, err := json.Marshal(s)
	if err != nil { t.Fatal(err) }
	var back Snapshot
	if err := json.Unmarshal(b, &back); err != nil { t.Fatal(err) }
	if back.WorldID != "W1" || back.Tick != 42 || len(back.Players) != 1 || back.Players[0].PlayerID != "u1" {
		t.Fatalf("快照回环不一致: %+v", back)
	}
}
