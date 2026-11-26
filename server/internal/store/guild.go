// 公会系统（v1.9.18）：服务端权威注册表，落盘 data/guilds.json。
// 设计要点：
//   - 玩家存档只存引用 GuildRef{ID,Name,Role}，成员明细/进度/等级都在注册表，避免多处数据漂移；
//   - 未加入公会的玩家 state.guild 为空 → 客户端展示"创建或加入"；
//   - 创建消耗 5,000 金币（服务端扣款），名称 2~12 字符且全局唯一；
//   - 公会等级 = 进度/1000 + 1；上限 30 人；会长退出自动转让给最早加入的成员，无人则解散。
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// GuildRef 玩家存档里的公会引用。
type GuildRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"` // president / member（officer 预留）
}

// GuildMemberInfo 公会成员明细（注册表内）。
type GuildMemberInfo struct {
	UserID   string `json:"userId"`
	Name     string `json:"name"`
	Level    int    `json:"level"`
	Role     string `json:"role"`
	Power    int    `json:"power"`    // 战力快照
	Donate   int    `json:"donate"`   // 累计捐献进度
	JoinedAt int64  `json:"joinedAt"`
}

// Guild 公会实体。
type Guild struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Progress  int               `json:"progress"`
	Members   []GuildMemberInfo `json:"members"`
	CreatedAt int64             `json:"createdAt"`
}

// Level 公会等级：每 1000 进度升 1 级。
func (g *Guild) Level() int { return g.Progress/1000 + 1 }

// GuildRegistry 全局注册表（单例，互斥持久化）。
type GuildRegistry struct {
	mu     sync.Mutex
	path   string
	guilds map[string]*Guild
}

var guildReg *GuildRegistry

// InitGuilds 在服务启动时调用（dataDir 与存档同目录）。
func InitGuilds(dataDir string) error {
	path := filepath.Join(dataDir, "guilds.json")
	reg := &GuildRegistry{path: path, guilds: map[string]*Guild{}}
	if b, err := os.ReadFile(path); err == nil {
		list := []*Guild{}
		if err := json.Unmarshal(b, &list); err != nil {
			return fmt.Errorf("公会数据损坏: %w", err)
		}
		for _, g := range list {
			reg.guilds[g.ID] = g
		}
	}
	guildReg = reg
	return nil
}

func (r *GuildRegistry) persistLocked() error {
	list := make([]*Guild, 0, len(r.guilds))
	for _, g := range r.guilds {
		list = append(list, g)
	}
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, r.path)
}

// Guilds 导出注册表句柄。
func Guilds() *GuildRegistry { return guildReg }

const (
	guildMaxMembers = 30
	guildCreateCost = 3000 // 创建公会消耗金币（v1.9.41 由 5000 下调）
)

// 供 net 包使用的导出常量与方法（appendLogs / guildCreateCost 为包内私有）。
const GuildCreateCost = guildCreateCost

// AppendLogs 写入玩家动态（net 包调用）。
func (p *PlayerState) AppendLogs(items []string) { p.appendLogs(items) }

// GuildList 按创建时间返回全部公会（加入页列表用）。
func (r *GuildRegistry) GuildList() []*Guild {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*Guild, 0, len(r.guilds))
	for _, g := range r.guilds {
		out = append(out, g)
	}
	// 按人数降序、同级按进度降序
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if len(out[j].Members) > len(out[i].Members) ||
				(len(out[j].Members) == len(out[i].Members) && out[j].Progress > out[i].Progress) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// GuildByID 查公会（含成员明细）。
func (r *GuildRegistry) GuildByID(id string) *Guild {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.guilds[id]
	if !ok {
		return nil
	}
	cp := *g
	return &cp
}

// GuildByUser 查玩家所在公会与角色（v1.9.39 公会分流服务端权威化）。未加入返回 nil。
func (r *GuildRegistry) GuildByUser(userID string) (*Guild, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, g := range r.guilds {
		for _, m := range g.Members {
			if m.UserID == userID {
				cp := *g
				return &cp, m.Role
			}
		}
	}
	return nil, ""
}

// GuildCreate 创建公会：唯一名校验 + 会长入会。返回创建好的公会。
func (r *GuildRegistry) GuildCreate(id, name, userID, userName string, level, power int) (*Guild, error) {
	name = strings.TrimSpace(name)
	if n := len([]rune(name)); n < 2 || n > 12 {
		return nil, fmt.Errorf("公会名需 2~12 个字符")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, g := range r.guilds {
		if g.Name == name {
			return nil, fmt.Errorf("公会名已被使用：%s", name)
		}
	}
	g := &Guild{
		ID:        id,
		Name:      name,
		CreatedAt: time.Now().Unix(),
		Members: []GuildMemberInfo{{
			UserID: userID, Name: userName, Level: level, Power: power,
			Role: "president", JoinedAt: time.Now().Unix(),
		}},
	}
	r.guilds[g.ID] = g
	if err := r.persistLocked(); err != nil {
		delete(r.guilds, g.ID)
		return nil, err
	}
	return g, nil
}

// GuildJoin 加入公会（满员拒绝）。
func (r *GuildRegistry) GuildJoin(guildID, userID, userName string, level, power int) (*Guild, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.guilds[guildID]
	if !ok {
		return nil, fmt.Errorf("公会不存在或已解散")
	}
	for _, m := range g.Members {
		if m.UserID == userID {
			return nil, fmt.Errorf("你已在该公会中")
		}
	}
	if len(g.Members) >= guildMaxMembers {
		return nil, fmt.Errorf("公会已满员（%d/%d）", guildMaxMembers, guildMaxMembers)
	}
	g.Members = append(g.Members, GuildMemberInfo{
		UserID: userID, Name: userName, Level: level, Power: power,
		Role: "member", JoinedAt: time.Now().Unix(),
	})
	if err := r.persistLocked(); err != nil {
		return nil, err
	}
	cp := *g
	return &cp, nil
}

// GuildLeave 退出公会：会长退出自动转让给最早加入的成员；最后一人退出则解散。返回残余公会（nil=已解散）。
func (r *GuildRegistry) GuildLeave(guildID, userID string) (*Guild, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.guilds[guildID]
	if !ok {
		return nil, fmt.Errorf("公会不存在或已解散")
	}
	idx := -1
	for i, m := range g.Members {
		if m.UserID == userID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, fmt.Errorf("你不在该公会中")
	}
	g.Members = append(g.Members[:idx], g.Members[idx+1:]...)
	if len(g.Members) == 0 {
		delete(r.guilds, guildID)
		if err := r.persistLocked(); err != nil {
			return nil, err
		}
		return nil, nil // 解散
	}
	if g.Members[0].Role != "president" {
		g.Members[0].Role = "president" // 最早加入的成员接任会长
	}
	if err := r.persistLocked(); err != nil {
		return nil, err
	}
	cp := *g
	return &cp, nil
}

// GuildDonate 公会捐献：进度+50，成员快照（等级/战力/累计捐献）刷新。返回进度与个人累计。
func (r *GuildRegistry) GuildDonate(guildID, userID string, level, power int) (progress, donated int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.guilds[guildID]
	if !ok {
		return 0, 0, fmt.Errorf("公会不存在或已解散")
	}
	found := false
	for i := range g.Members {
		if g.Members[i].UserID == userID {
			g.Members[i].Level = level
			g.Members[i].Power = power
			g.Members[i].Donate += 50
			donated = g.Members[i].Donate
			found = true
			break
		}
	}
	if !found {
		return 0, 0, fmt.Errorf("你不在该公会中")
	}
	g.Progress += 50
	progress = g.Progress
	if err := r.persistLocked(); err != nil {
		return 0, 0, err
	}
	return progress, donated, nil
}
