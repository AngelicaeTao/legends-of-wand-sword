package game

import (
	"math"
	"time"
)

// IdleInput 离线结算输入。now 与 lastSeen 均为服务端时间，客户端时间戳一概不采信。
type IdleInput struct {
	Now      int64 `json:"now"`      // 服务端 Unix 秒
	LastSeen int64 `json:"lastSeen"` // 服务端存档中的 lastSeen
	Level    int   `json:"level"`
}
type IdleResult struct {
	Minutes  float64 `json:"minutes"`
	Hours    float64 `json:"hours"`
	Capped   bool    `json:"capped"`
	Exp      int     `json:"exp"`
	Gold     int     `json:"gold"`
	Material int     `json:"material"`
	Potions  int     `json:"potions"` // v1.9.27 离线挂机产出的回血药水数量
	Log      string  `json:"log"`
}

// SettleIdle 按服务端时间差结算离线收益，封顶8h。
// 首次进入 lastSeen=0 时按0处理；防系统时间改刷（只认服务端 now）。
func SettleIdle(in IdleInput) IdleResult {
	cfg := C().Economy.Idle
	capHours := num(cfg["maxHours"])
	eff := num(cfg["efficiency"])
	expRate := num(cfg["expPerMinute"])
	goldRate := num(cfg["goldPerMinute"])
	matRate := num(cfg["materialPerMinute"])

	secs := in.Now - in.LastSeen
	if in.LastSeen <= 0 || secs < 0 {
		secs = 0
	}
	minutes := float64(secs) / 60
	capped := false
	if capHours > 0 && minutes > capHours*60 {
		minutes = capHours * 60
		capped = true
	}
	lvMult := 1 + float64(in.Level-1)*0.05
	// v1.9.27 离线挂机额外产出回血药水（每 30 分钟 1 瓶，向上取整；8h 封顶即最多 16 瓶）
	potionRate := num(cfg["potionPerHalfHour"])
	if potionRate <= 0 {
		potionRate = 1
	}
	potions := int(math.Ceil(minutes / 30 * potionRate))
	r := IdleResult{
		Minutes:  math.Round(minutes),
		Hours:    math.Round(minutes/60*100) / 100,
		Capped:   capped,
		Exp:      int(math.Round(minutes * expRate * eff * lvMult)),
		Gold:     int(math.Round(minutes * goldRate * eff * lvMult)),
		Material: int(math.Round(minutes * matRate * eff)),
		Potions:  potions,
	}
	r.Log = "服务端按 lastSeen 时间差结算；客户端改系统时间无效"
	if capped {
		r.Log += "；已达8小时上限按上限结算"
	}
	return r
}

// ServerNow 返回服务端当前 Unix 秒（结算唯一可信时间源）。
func ServerNow() int64 { return time.Now().Unix() }
