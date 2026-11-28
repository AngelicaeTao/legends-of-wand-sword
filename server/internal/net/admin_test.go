package net

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func newAdminTestStore(t *testing.T) (*AccountStore, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := NewAccountStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := hashPassword("old-secret")
	if err != nil {
		t.Fatal(err)
	}
	account := &Account{
		UserID: "u_admin_test", Username: "TestUser", Password: hash,
		DeviceIDs: []string{"dev_admin_test"}, CreatedAt: 123,
	}
	if err := store.put(account); err != nil {
		t.Fatal(err)
	}
	store.byName["testuser"] = account.UserID
	store.byDev["dev_admin_test"] = account.UserID
	return store, dir
}

func performAdminRequest(handler http.Handler, method, path, remoteAddr, body, contentType string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = remoteAddr
	req.Host = "127.0.0.1:3000"
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func performAdminTestLogin(t *testing.T, password string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]string{"username": "TestUser", "password": password})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	loginAccount(rec, req)
	return rec
}

func TestAdminAllowsLoopbackOnly(t *testing.T) {
	store, _ := newAdminTestStore(t)
	handler := newAdminHandler(store)
	for _, remoteAddr := range []string{"127.0.0.1:12345", "127.0.2.3:12345", "[::1]:12345"} {
		t.Run("allow "+remoteAddr, func(t *testing.T) {
			rec := performAdminRequest(handler, http.MethodGet, "/admin", remoteAddr, "", "")
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("admin response must not be cached")
			}
			if rec.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("admin response must not enable CORS")
			}
		})
	}
	for _, remoteAddr := range []string{"192.0.2.10:12345", "[2001:db8::1]:12345", "", "127.0.0.1", "not-an-ip:12345"} {
		t.Run("reject "+remoteAddr, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/admin", nil)
			req.RemoteAddr = remoteAddr
			req.Host = "127.0.0.1:3000"
			req.Header.Set("X-Forwarded-For", "127.0.0.1")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", rec.Code)
			}
		})
	}
}

func TestAdminRejectsProxyHeadersAndPublicHost(t *testing.T) {
	store, _ := newAdminTestStore(t)
	handler := newAdminHandler(store)
	tests := []struct {
		name, host, header, value string
		want                      int
	}{
		{"localhost", "localhost:3000", "", "", http.StatusOK},
		{"IPv6 loopback host", "[::1]:3000", "", "", http.StatusOK},
		{"public host", "game.nantong519.dpdns.org", "", "", http.StatusForbidden},
		{"Forwarded", "127.0.0.1:3000", "Forwarded", "for=203.0.113.8", http.StatusForbidden},
		{"X-Forwarded-For", "127.0.0.1:3000", "X-Forwarded-For", "203.0.113.8", http.StatusForbidden},
		{"X-Real-IP", "127.0.0.1:3000", "X-Real-IP", "203.0.113.8", http.StatusForbidden},
		{"Cloudflare", "127.0.0.1:3000", "Cf-Connecting-IP", "203.0.113.8", http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/admin", nil)
			req.RemoteAddr = "127.0.0.1:12345"
			req.Host = test.host
			if test.header != "" {
				req.Header.Set(test.header, test.value)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != test.want {
				t.Fatalf("status = %d, want %d", rec.Code, test.want)
			}
		})
	}
}

func TestAdminRejectsTunneledPasswordReset(t *testing.T) {
	store, _ := newAdminTestStore(t)
	handler := newAdminHandler(store)
	before, err := store.get("u_admin_test")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/api/password", strings.NewReader(`{"username":"TestUser","newPassword":"new-secret"}`))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Host = "game.nantong519.dpdns.org"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", "203.0.113.8")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	after, err := store.get("u_admin_test")
	if err != nil {
		t.Fatal(err)
	}
	if before.Password != after.Password {
		t.Fatal("remote request changed the password hash")
	}
}

func TestAdminMethodAndRouteLimits(t *testing.T) {
	store, _ := newAdminTestStore(t)
	handler := newAdminHandler(store)
	tests := []struct {
		method, path, allow string
		want                int
	}{
		{http.MethodPost, "/admin", http.MethodGet, http.StatusMethodNotAllowed},
		{http.MethodGet, "/admin/api/password", http.MethodPost, http.StatusMethodNotAllowed},
		{http.MethodOptions, "/admin/api/password", http.MethodPost, http.StatusMethodNotAllowed},
		{http.MethodGet, "/admin/unknown", "", http.StatusNotFound},
	}
	for _, test := range tests {
		rec := performAdminRequest(handler, test.method, test.path, "127.0.0.1:12345", "", "")
		if rec.Code != test.want {
			t.Errorf("%s %s status = %d, want %d", test.method, test.path, rec.Code, test.want)
		}
		if test.allow != "" && rec.Header().Get("Allow") != test.allow {
			t.Errorf("%s %s Allow = %q, want %q", test.method, test.path, rec.Header().Get("Allow"), test.allow)
		}
	}
}

func TestAdminResetPasswordAndLogin(t *testing.T) {
	store, dir := newAdminTestStore(t)
	handler := newAdminHandler(store)
	rec := performAdminRequest(handler, http.MethodPost, "/admin/api/password", "127.0.0.1:12345", `{"username":" TestUser ","newPassword":"new-secret"}`, "application/json; charset=utf-8")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	stored, err := store.get("u_admin_test")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rec.Body.String(), "new-secret") || strings.Contains(rec.Body.String(), stored.Password) {
		t.Fatal("response exposed the new password or its hash")
	}
	if checkPassword(stored.Password, "old-secret") || !checkPassword(stored.Password, "new-secret") {
		t.Fatal("stored password hash does not match the expected new password")
	}
	if stored.Password == "new-secret" {
		t.Fatal("password was stored in plaintext")
	}
	if stored.CreatedAt != 123 || len(stored.DeviceIDs) != 1 || stored.DeviceIDs[0] != "dev_admin_test" {
		t.Fatal("password reset changed unrelated account fields")
	}

	reloaded, err := NewAccountStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := reloaded.get("u_admin_test")
	if err != nil {
		t.Fatal(err)
	}
	if persisted == nil || !checkPassword(persisted.Password, "new-secret") {
		t.Fatal("new password hash was not persisted")
	}

	previousAccounts, previousSecret := accounts, tokenSecret
	accounts = store
	tokenSecret = []byte("test-token-secret")
	t.Cleanup(func() {
		accounts = previousAccounts
		tokenSecret = previousSecret
	})
	if login := performAdminTestLogin(t, "old-secret"); login.Code != http.StatusUnauthorized {
		t.Fatalf("old password login status = %d, want 401", login.Code)
	}
	if login := performAdminTestLogin(t, "new-secret"); login.Code != http.StatusOK {
		t.Fatalf("new password login status = %d, body = %s", login.Code, login.Body.String())
	}
}

func TestAdminResetPasswordValidation(t *testing.T) {
	store, dir := newAdminTestStore(t)
	handler := newAdminHandler(store)
	tests := []struct {
		name, body, contentType string
		want                    int
	}{
		{"unknown user", `{"username":"Nobody","newPassword":"valid-pass"}`, "application/json", http.StatusNotFound},
		{"short username", `{"username":"x","newPassword":"valid-pass"}`, "application/json", http.StatusBadRequest},
		{"short password", `{"username":"TestUser","newPassword":"123"}`, "application/json", http.StatusBadRequest},
		{"empty body", "", "application/json", http.StatusBadRequest},
		{"malformed JSON", `{`, "application/json", http.StatusBadRequest},
		{"multiple JSON values", `{} {}`, "application/json", http.StatusBadRequest},
		{"wrong content type", `{"username":"TestUser","newPassword":"new-secret"}`, "text/plain", http.StatusUnsupportedMediaType},
		{"oversized body", `{"username":"TestUser","newPassword":"` + strings.Repeat("x", 5000) + `"}`, "application/json", http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rec := performAdminRequest(handler, http.MethodPost, "/admin/api/password", "127.0.0.1:12345", test.body, test.contentType)
			if rec.Code != test.want {
				t.Fatalf("status = %d, want %d", rec.Code, test.want)
			}
			account, err := store.get("u_admin_test")
			if err != nil {
				t.Fatal(err)
			}
			if !checkPassword(account.Password, "old-secret") {
				t.Fatal("failed request changed the original password")
			}
		})
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("account file count = %d, want 1", len(entries))
	}
}
