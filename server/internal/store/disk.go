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
)

// DiskStore 是线程安全的本地存档。
type DiskStore struct {
	dir string
	mu  sync.RWMutex
}

func NewDiskStore(dir string) (*DiskStore, error) {
	path := filepath.Join(dir, "saves")
	if err := os.MkdirAll(path, 0755); err != nil {
		return nil, err
	}
	return &DiskStore{dir: path}, nil
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
func (d *DiskStore) Load(userID string) (*PlayerState, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	b, err := os.ReadFile(d.path(userID))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p PlayerState
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("存档损坏: %w", err)
	}
	return &p, nil
}

// Save 原子写入（先写临时文件再 rename），避免写一半导致存档损坏。
func (d *DiskStore) Save(userID string, p *PlayerState) error {
	d.mu.Lock()
	defer d.mu.Unlock()
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

// List 列出所有存档用户（管理工具用）。
func (d *DiskStore) List() ([]string, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
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
