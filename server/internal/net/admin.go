package net

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	stdnet "net"
	"net/http"
	"strings"
)

func newAdminHandler(store *AccountStore) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, adminPage)
	})
	mux.HandleFunc("/admin/api/password", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
			return
		}
		adminResetPassword(store, w, r)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !isLoopbackRequest(r) {
			http.Error(w, "仅限服务器本机访问", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func isLoopbackRequest(r *http.Request) bool {
	host, _, err := stdnet.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := stdnet.ParseIP(host)
	if ip == nil || !ip.IsLoopback() || !isLoopbackHost(r.Host) {
		return false
	}
	// 本机反向代理/Cloudflare 隧道不是用户直接本机访问。
	for _, header := range []string{"Forwarded", "X-Forwarded-For", "X-Real-IP", "Cf-Connecting-IP"} {
		if r.Header.Get(header) != "" {
			return false
		}
	}
	return true
}

func isLoopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := stdnet.SplitHostPort(hostport); err == nil {
		host = h
	} else {
		host = strings.Trim(hostport, "[]")
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := stdnet.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func adminResetPassword(store *AccountStore, w http.ResponseWriter, r *http.Request) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		http.Error(w, "Content-Type 必须为 application/json", http.StatusUnsupportedMediaType)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var q struct {
		Username    string `json:"username"`
		NewPassword string `json:"newPassword"`
	}
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&q); err != nil {
		http.Error(w, "无效的 JSON 请求", http.StatusBadRequest)
		return
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		http.Error(w, "请求体只能包含一个 JSON 对象", http.StatusBadRequest)
		return
	}
	q.Username = strings.TrimSpace(q.Username)
	if len(q.Username) < 2 || len(q.NewPassword) < 6 {
		http.Error(w, "用户名≥2字符、密码≥6字符", http.StatusBadRequest)
		return
	}
	if err := store.resetPassword(q.Username, q.NewPassword); err != nil {
		if errors.Is(err, errAccountNotFound) {
			http.Error(w, "账号不存在", http.StatusNotFound)
			return
		}
		http.Error(w, "密码修改失败", http.StatusInternalServerError)
		return
	}
	jsonOut(w, map[string]any{"ok": true})
}

const adminPage = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>账号管理</title>
<style>
:root{color-scheme:dark;font-family:system-ui,-apple-system,"Segoe UI",sans-serif;background:#10141c;color:#e8edf5}
*{box-sizing:border-box}body{margin:0;min-height:100vh;display:grid;place-items:center;padding:24px}
main{width:min(100%,420px);padding:32px;border:1px solid #2a3445;border-radius:16px;background:#171d28;box-shadow:0 20px 60px #0005}
h1{margin:0 0 8px;font-size:26px}p{margin:0 0 24px;color:#9eacbf}form{display:grid;gap:18px}
label{display:grid;gap:8px;font-size:14px;color:#bcc8d8}input{width:100%;padding:12px 14px;border:1px solid #354156;border-radius:9px;background:#0f141d;color:#fff;font:inherit}
input:focus{outline:2px solid #5d8dff;outline-offset:1px}button{padding:12px;border:0;border-radius:9px;background:#5d8dff;color:#091427;font-weight:700;font:inherit;cursor:pointer}
button:disabled{opacity:.6;cursor:wait}#result{min-height:20px;margin:0;font-size:14px}.success{color:#78dca4}.error{color:#ff9191}
</style>
</head>
<body>
<main>
<h1>账号管理</h1>
<p>仅限服务器本机访问，可重置指定账号的密码。</p>
<form id="password-form">
<label>用户名<input id="username" name="username" required minlength="2" autocomplete="off"></label>
<label>新密码<input id="new-password" name="newPassword" type="password" required minlength="6" autocomplete="new-password"></label>
<button type="submit">修改密码</button>
<p id="result" role="status"></p>
</form>
</main>
<script>
const form = document.getElementById('password-form');
const username = document.getElementById('username');
const password = document.getElementById('new-password');
const result = document.getElementById('result');
form.addEventListener('submit', async event => {
  event.preventDefault();
  const button = form.querySelector('button');
  button.disabled = true;
  result.className = '';
  result.textContent = '正在修改…';
  try {
    const response = await fetch('/admin/api/password', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({username: username.value, newPassword: password.value})
    });
    if (!response.ok) {
      const message = await response.text();
      throw new Error(message.trim() || '密码修改失败');
    }
    password.value = '';
    result.className = 'success';
    result.textContent = '密码修改成功';
  } catch (error) {
    result.className = 'error';
    result.textContent = error.message;
  } finally {
    button.disabled = false;
  }
});
</script>
</body>
</html>`
