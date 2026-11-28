// 磁盘存档：standalone 初跑模式的持久化实现。
// 每个用户一个 JSON 文件，落在 data/saves/ 下——迁移到 Nakama Storage 时，
// 只需替换 Save/Load 实现，PlayerState 与合并逻辑完全复用。
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// DiskStore 是线程安全的本地存档。
// v1.9.41 交互延迟优化：Load 走内存缓存（免每请求读盘+解析 JSON），
// Save 只更新缓存与 dirty 集合立即返回，由后台协程每 400ms 批量原子落盘
// （先写临时文件再 rename）。同一 userID 只保留最新状态，连续动作合并为一次写盘。
type DiskStore struct {
	dir    string
	mu     sync.Mutex
	cache  map[string]*PlayerState        // uid → 最新状态（读缓存，进程内单一事实源）
	dirty  map[string]struct{}            // 待落盘 uid 集合
	stopCh chan struct{}
}

func NewDiskStore(dir string) (*DiskStore, error) {
	path := filepath.Join(dir, "saves")
	if err := os.MkdirAll(path, 0755); err != nil {
		return nil, err
	}
	d := &DiskStore{
		dir:    path,
		cache:  map[string]*PlayerState{},
		dirty:  map[string]struct{}{},
		stopCh: make(chan struct{}),
	}
	go d.flushLoop()
	return d, nil
}

// flushLoop 后台去抖落盘：每 400ms 把 dirty 集合写盘一次。
func (d *DiskStore) flushLoop() {
	t := time.NewTicker(400 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			_ = d.FlushAll()
		case <-d.stopCh:
			return
		}
	}
}

// FlushAll 立即落盘所有 dirty 存档（优雅关闭 / 关键节点调用）。
func (d *DiskStore) FlushAll() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for uid := range d.dirty {
		if err := d.writeLocked(uid, d.cache[uid]); err != nil {
			return err
		}
		delete(d.dirty, uid)
	}
	return nil
}

// writeLocked 原子写入（调用方必须已持有 d.mu）。
func (d *DiskStore) writeLocked(userID string, p *PlayerState) error {
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	tmp := d.path(userID) + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, d.path(userID))
}

func (d *DiskStore) path(userID string) string {
	// 过滤路径分隔符，防目录穿越
	safe := ""
	for _, r := range userID {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			safe += string(r)
		} else {
			safe += "_"
		}
	}
	if safe == "" {
		safe = "anonymous"
	}
	return filepath.Join(d.dir, safe+".json")
}

// Load 读存档；不存在返回 (nil, nil)，由调用方决定创角。
// v1.9.41：优先命中内存缓存（进程内单一事实源），未命中才读盘。
func (d *DiskStore) Load(userID string) (*PlayerState, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if p, ok := d.cache[userID]; ok {
		return p, nil
	}
	b, err := os.ReadFile(d.path(userID))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p PlayerState
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("角色数据读取失败: %w", err)
	}
	d.cache[userID] = &p
	return &p, nil
}

// Save 更新缓存与 dirty 集合，立即返回；实际落盘由后台 flushLoop 批量完成。
func (d *DiskStore) Save(userID string, p *PlayerState) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cache[userID] = p
	d.dirty[userID] = struct{}{}
	return nil
}

// List 列出所有存档用户（管理工具用）。
func (d *DiskStore) List() ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	entries, err := os.ReadDir(d.dir)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
			out = append(out, e.Name()[:len(e.Name())-5])
		}
	}
	return out, nil
}
