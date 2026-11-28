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
// v1.9.86 并发加固：缓存改为「规范 JSON 字节」，Load 每次 unmarshal 出新对象返回，
// 杜绝多个请求共享同一个 *PlayerState 指针导致的数据竞争 / 相互覆盖。
// Save 只更新缓存字节与 dirty 集合立即返回，由后台协程每 400ms 批量原子落盘。
type DiskStore struct {
	dir      string
	mu       sync.Mutex
	cache    map[string][]byte       // uid → 规范 JSON 字节（进程内单一事实源）
	lastUsed map[string]time.Time    // uid → 最近访问时间（用于清理冷缓存）
	dirty    map[string]struct{}     // 待落盘 uid 集合
	stopCh   chan struct{}
}

func NewDiskStore(dir string) (*DiskStore, error) {
	path := filepath.Join(dir, "saves")
	if err := os.MkdirAll(path, 0755); err != nil {
		return nil, err
	}
	d := &DiskStore{
		dir:      path,
		cache:    map[string][]byte{},
		lastUsed: map[string]time.Time{},
		dirty:    map[string]struct{}{},
		stopCh:   make(chan struct{}),
	}
	go d.flushLoop()
	return d, nil
}

// flushLoop 后台去抖落盘：每 400ms 把 dirty 集合写盘一次；
// 每 60s 清理 lastUsed 超过 30 分钟且不在 dirty 的冷缓存，防止内存无限增长。
func (d *DiskStore) flushLoop() {
	t := time.NewTicker(400 * time.Millisecond)
	defer t.Stop()
	clean := time.NewTicker(60 * time.Second)
	defer clean.Stop()
	for {
		select {
		case <-t.C:
			_ = d.FlushAll()
		case <-clean.C:
			d.pruneCache()
		case <-d.stopCh:
			return
		}
	}
}

// pruneCache 清理长时间未访问且无未落盘变更的缓存。
func (d *DiskStore) pruneCache() {
	d.mu.Lock()
	defer d.mu.Unlock()
	cutoff := time.Now().Add(-30 * time.Minute)
	for uid, t := range d.lastUsed {
		if t.After(cutoff) {
			continue
		}
		if _, isDirty := d.dirty[uid]; isDirty {
			continue
		}
		delete(d.cache, uid)
		delete(d.lastUsed, uid)
	}
}

// FlushAll 立即落盘所有 dirty 存档（优雅关闭 / 关键节点调用）。
func (d *DiskStore) FlushAll() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for uid := range d.dirty {
		b, ok := d.cache[uid]
		if !ok {
			delete(d.dirty, uid)
			continue
		}
		if err := d.writeBytesLocked(uid, b); err != nil {
			return err
		}
		delete(d.dirty, uid)
	}
	return nil
}

// writeBytesLocked 原子写入（调用方必须已持有 d.mu）。
func (d *DiskStore) writeBytesLocked(userID string, b []byte) error {
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
// 命中缓存时从缓存字节 unmarshal 出新对象返回（不共享指针），未命中才读盘。
func (d *DiskStore) Load(userID string) (*PlayerState, error) {
	d.mu.Lock()
	b, ok := d.cache[userID]
	if ok {
		d.lastUsed[userID] = time.Now()
	}
	d.mu.Unlock()
	if !ok {
		var err error
		b, err = os.ReadFile(d.path(userID))
		if os.IsNotExist(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		// 读盘期间可能已有 Save 更新了缓存，以缓存为准，避免返回过期数据
		d.mu.Lock()
		if cb, cached := d.cache[userID]; cached {
			b = cb
		} else {
			d.cache[userID] = append([]byte(nil), b...)
			d.lastUsed[userID] = time.Now()
		}
		d.mu.Unlock()
	}
	var p PlayerState
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("角色数据读取失败: %w", err)
	}
	return &p, nil
}

// Save 更新缓存字节与 dirty 集合，立即返回；实际落盘由后台 flushLoop 批量完成。
// 缓存只存字节，避免把调用方持有的对象指针放进全局缓存。
func (d *DiskStore) Save(userID string, p *PlayerState) error {
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	d.mu.Lock()
	d.cache[userID] = b
	d.lastUsed[userID] = time.Now()
	d.dirty[userID] = struct{}{}
	d.mu.Unlock()
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
