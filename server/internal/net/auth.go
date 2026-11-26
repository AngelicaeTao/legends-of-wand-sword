// Package net · 账号体系（Nakama 语义同构，单端口磁盘版）。
// 提供两条登录路径：设备账号（device_id）+ 用户名密码；签发 token，
// 存档按服务端 userId 隔离，实现跨设备续玩。后续可无缝迁到真 Nakama。
package net

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Account 账号记录（存账号库，与 PlayerState 分离）。
type Account struct {
	UserID     string `json:"userId"`
	Username   string `json:"username,omitempty"`
	Password   string `json:"password,omitempty"` // bcrypt hash
	DeviceIDs  []string `json:"deviceIds,omitempty"`
	CreatedAt  int64  `json:"createdAt"`
	LastLogin  int64  `json:"lastLogin"`
}

// AccountStore 线程安全的磁盘账号库。
type AccountStore struct {
	dir   string
	mu    sync.RWMutex
	byName map[string]string // username -> userId
	byDev  map[string]string // device_id -> userId
}

func NewAccountStore(dataDir string) (*AccountStore, error) {
	dir := filepath.Join(dataDir, "accounts")
	if err := os.MkdirAll(dir, 0755); err != nil { return nil, err }
	s := &AccountStore{dir: dir, byName: map[string]string{}, byDev: map[string]string{}}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" { continue }
		b, err := os.ReadFile(filepath.Join(dir, e.Name())); if err != nil { continue }
		var a Account
		if json.Unmarshal(b, &a) != nil { continue }
		if a.Username != "" { s.byName[strings.ToLower(a.Username)] = a.UserID }
		for _, d := range a.DeviceIDs { s.byDev[d] = a.UserID }
	}
	return s, nil
}

func (s *AccountStore) path(userID string) string { return filepath.Join(s.dir, safeName(userID)+".json") }

func (s *AccountStore) get(userID string) (*Account, error) {
	b, err := os.ReadFile(s.path(userID))
	if os.IsNotExist(err) { return nil, nil }
	if err != nil { return nil, err }
	var a Account
	if err := json.Unmarshal(b, &a); err != nil { return nil, err }
	return &a, nil
}
func (s *AccountStore) put(a *Account) error {
	b, err := json.MarshalIndent(a, "", "  "); if err != nil { return err }
	tmp := s.path(a.UserID) + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil { return err }
	return os.Rename(tmp, s.path(a.UserID))
}

func safeName(s string) string {
	out := ""
	for _, r := range s {
		if (r>='a'&&r<='z')||(r>='A'&&r<='Z')||(r>='0'&&r<='9')||r=='_'||r=='-' { out += string(r) } else { out += "_" }
	}
	if out == "" { out = "u" }
	return out
}

func newUserID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "u_" + hex.EncodeToString(b)
}

// accounts 全局实例（HandlerWithWeb 中初始化）
var accounts *AccountStore
var tokenSecret []byte

func initTokenSecret(dataDir string) {
	// 内测原型：用固定派生密钥，保证多实例/重启后 token 验证一致。
	// （生产环境应换成从安全配置注入；此处单端口内测优先保证跨设备续玩稳定）
	sum := sha1.Sum([]byte("zjcs-nakama-compatible-internal-token-secret-v1"))
	tokenSecret = sum[:]
	// 仍尝试写文件以兼容旧逻辑，但不依赖它（避免多实例各自随机导致 token 失效）
	p := filepath.Join(dataDir, "token_secret")
	_ = os.WriteFile(p, tokenSecret, 0600)
}

// signToken 生成 HMAC token：base64(userId.exp.sig)
func signToken(userID string) string {
	exp := time.Now().Add(30 * 24 * time.Hour).Unix()
	payload := fmt.Sprintf("%s.%d", userID, exp)
	m := hmac.New(sha256.New, tokenSecret)
	m.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(m.Sum(nil))
	return base64.RawURLEncoding.EncodeToString([]byte(payload+"."+sig))
}

// verifyToken 校验 token，返回 userId。
func verifyToken(token string) (string, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(token); if err != nil { return "", false }
	parts := strings.Split(string(raw), ".")
	if len(parts) != 3 { return "", false }
	payload := parts[0] + "." + parts[1]
	m := hmac.New(sha256.New, tokenSecret)
	m.Write([]byte(payload))
	want := base64.RawURLEncoding.EncodeToString(m.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(parts[2])) { return "", false }
	var exp int64
	if _, err := fmt.Sscanf(parts[1], "%d", &exp); err != nil { return "", false }
	if time.Now().Unix() > exp { return "", false }
	return parts[0], true
}

// ---------- HTTP handlers ----------

// deviceLogin：设备账号登录（零输入）。deviceId 不存在则自动注册。
func deviceLogin(w http.ResponseWriter, r *http.Request) {
	var q struct{ DeviceID string `json:"deviceId"` }
	_ = json.NewDecoder(r.Body).Decode(&q)
	if q.DeviceID == "" { http.Error(w, "deviceId 不能为空", 400); return }
	accounts.mu.Lock()
	defer accounts.mu.Unlock()
	uid, ok := accounts.byDev[q.DeviceID]
	if !ok {
		uid = newUserID()
		a := &Account{UserID: uid, DeviceIDs: []string{q.DeviceID}, CreatedAt: time.Now().Unix()}
		if err := accounts.put(a); err != nil { http.Error(w, err.Error(), 500); return }
		accounts.byDev[q.DeviceID] = uid
	} else {
		a, _ := accounts.get(uid)
		if a != nil { a.LastLogin = time.Now().Unix(); _ = accounts.put(a) }
	}
	jsonOut(w, map[string]any{"userId": uid, "token": signToken(uid), "username": ""})
}

// registerAccount：用户名密码注册。
func registerAccount(w http.ResponseWriter, r *http.Request) {
	var q struct{ Username, Password, DeviceID string }
	_ = json.NewDecoder(r.Body).Decode(&q)
	q.Username = strings.TrimSpace(q.Username)
	if len(q.Username) < 2 || len(q.Password) < 4 { http.Error(w, "用户名≥2字符、密码≥4字符", 400); return }
	accounts.mu.Lock()
	defer accounts.mu.Unlock()
	if _, exists := accounts.byName[strings.ToLower(q.Username)]; exists {
		http.Error(w, "用户名已存在", 409); return
	}
	hash, err := hashPassword(q.Password)
	if err != nil { http.Error(w, err.Error(), 500); return }
	uid := newUserID()
	a := &Account{UserID: uid, Username: q.Username, Password: hash, CreatedAt: time.Now().Unix(), LastLogin: time.Now().Unix()}
	if q.DeviceID != "" { a.DeviceIDs = []string{q.DeviceID} }
	if err := accounts.put(a); err != nil { http.Error(w, err.Error(), 500); return }
	accounts.byName[strings.ToLower(q.Username)] = uid
	if q.DeviceID != "" { accounts.byDev[q.DeviceID] = uid }
	jsonOut(w, map[string]any{"userId": uid, "token": signToken(uid), "username": q.Username})
}

// loginAccount：用户名密码登录。
func loginAccount(w http.ResponseWriter, r *http.Request) {
	var q struct{ Username, Password, DeviceID string }
	_ = json.NewDecoder(r.Body).Decode(&q)
	accounts.mu.RLock()
	uid, ok := accounts.byName[strings.ToLower(strings.TrimSpace(q.Username))]
	accounts.mu.RUnlock()
	if !ok { http.Error(w, "用户不存在", 404); return }
	a, _ := accounts.get(uid)
	if a == nil || !checkPassword(a.Password, q.Password) {
		http.Error(w, "密码错误", 401); return
	}
	accounts.mu.Lock()
	a.LastLogin = time.Now().Unix()
	if q.DeviceID != "" && !contains(a.DeviceIDs, q.DeviceID) { a.DeviceIDs = append(a.DeviceIDs, q.DeviceID); accounts.byDev[q.DeviceID] = uid }
	_ = accounts.put(a)
	accounts.mu.Unlock()
	jsonOut(w, map[string]any{"userId": uid, "token": signToken(uid), "username": a.Username})
}

// bindDevice：已登录用户绑定设备（换设备续玩用）。
func bindDevice(w http.ResponseWriter, r *http.Request) {
	uid, ok := authUserID(r)
	if !ok { http.Error(w, "未授权", 401); return }
	var q struct{ DeviceID string `json:"deviceId"` }
	_ = json.NewDecoder(r.Body).Decode(&q)
	if q.DeviceID == "" { http.Error(w, "deviceId 不能为空", 400); return }
	accounts.mu.Lock(); defer accounts.mu.Unlock()
	a, _ := accounts.get(uid)
	if a == nil { http.Error(w, "用户不存在", 404); return }
	if !contains(a.DeviceIDs, q.DeviceID) { a.DeviceIDs = append(a.DeviceIDs, q.DeviceID) }
	_ = accounts.put(a)
	accounts.byDev[q.DeviceID] = uid
	jsonOut(w, map[string]any{"ok": true, "userId": uid})
}

// authUserID 从 Authorization: Bearer <token> 或 ?token= 提取 userId。
func authUserID(r *http.Request) (string, bool) {
	t := ""
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") { t = strings.TrimPrefix(h, "Bearer ") }
	if t == "" { t = r.URL.Query().Get("token") }
	if t == "" { return "", false }
	return verifyToken(t)
}

func contains(ss []string, s string) bool { for _, x := range ss { if x == s { return true } }; return false }
