package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/linuxsuren/open-cloud-web/internal/auth"
	"github.com/linuxsuren/open-cloud-web/internal/model"
	"github.com/linuxsuren/open-cloud-web/internal/secrets"
)

func doJSON(t *testing.T, h http.Handler, method, target, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, target, rdr)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
}

func TestHealthz(t *testing.T) {
	_, _, h := newTestServer(t)
	rec := doJSON(t, h.Routes(), "GET", "/healthz", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz: %d", rec.Code)
	}
	if rec.Header().Get("X-Request-ID") == "" {
		t.Fatal("missing X-Request-ID header")
	}
}

func TestUnauthenticated401(t *testing.T) {
	_, _, h := newTestServer(t)
	for _, target := range []string{"/api/v1/me", "/api/v1/instances", "/api/v1/auth/pats", "/api/v1/users", "/api/v1/admin/audit-logs"} {
		rec := doJSON(t, h.Routes(), "GET", target, "", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s: want 401 got %d", target, rec.Code)
		}
		var e map[string]string
		decodeBody(t, rec, &e)
		if e["error"] == "" {
			t.Fatalf("%s: missing {error} body", target)
		}
	}
}

func TestNonAdminForbidden(t *testing.T) {
	store, _, h := newTestServer(t)
	tok := seedUser(t, store, h, "user", 0)
	for _, target := range []string{"/api/v1/users", "/api/v1/admin/audit-logs"} {
		rec := doJSON(t, h.Routes(), "GET", target, tok, nil)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s: want 403 got %d", target, rec.Code)
		}
	}
	rec := doJSON(t, h.Routes(), "POST", "/api/v1/users", tok, map[string]string{"username": "x", "password": "longenough"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("create user: want 403 got %d", rec.Code)
	}
}

func TestCreateInstanceDurationValidation(t *testing.T) {
	store, _, h := newTestServer(t)
	tok := seedUser(t, store, h, "user", 0)
	acctID := seedAccount(t, store)
	valid := map[string]any{"cloudAccountID": acctID, "region": "cn-beijing", "zone": "a", "imageID": "img-1", "instanceType": "small"}

	// 超全局上限（7200）。
	rec := doJSON(t, h.Routes(), "POST", "/api/v1/instances", tok, withField(valid, "durationSec", 999999))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("over cap: want 400 got %d", rec.Code)
	}
	// 非正数。
	rec = doJSON(t, h.Routes(), "POST", "/api/v1/instances", tok, withField(valid, "durationSec", -5))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("negative: want 400 got %d", rec.Code)
	}
	// 用户级上限（1800）覆盖全局。
	u := store.users[7]
	u.MaxDurationSec = 1800
	rec = doJSON(t, h.Routes(), "POST", "/api/v1/instances", tok, withField(valid, "durationSec", 3600))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("user cap: want 400 got %d", rec.Code)
	}
	u.MaxDurationSec = 0
	// 合法默认时长。
	rec = doJSON(t, h.Routes(), "POST", "/api/v1/instances", tok, valid)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("valid: want 202 got %d body=%s", rec.Code, rec.Body.String())
	}
}


// seedAccount 为用户 7 创建一个可用云账号，返回其 ID。
func seedAccount(t *testing.T, store *fakeStore) int64 {
	t.Helper()
	enc, err := secrets.Encrypt("test-secret-key-123", "test-enc-secret")
	if err != nil {
		t.Fatal(err)
	}
	a := &model.CloudAccount{UserID: 7, Name: "acct", Provider: "alicloud", AccessKey: "AK", SecretEnc: enc}
	if err := store.CreateCloudAccount(a); err != nil {
		t.Fatal(err)
	}
	return a.ID
}

func withField(base map[string]any, key string, v any) map[string]any {
	out := map[string]any{}
	for k, vv := range base {
		out[k] = vv
	}
	out[key] = v
	return out
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func TestInstanceLifecycleApplyRenewDestroy(t *testing.T) {
	store, runner, h := newTestServer(t)
	tok := seedUser(t, store, h, "user", 0)
	srv := h.Routes()

	acctID := seedAccount(t, store)
	body := map[string]any{"cloudAccountID": acctID, "region": "cn-beijing", "zone": "a",
		"imageID": "img-1", "instanceType": "small", "durationSec": 3600, "name": "lab-1"}
	rec := doJSON(t, srv, "POST", "/api/v1/instances", tok, body)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var created Instance
	decodeBody(t, rec, &created)
	if created.Status != StatusCreating || created.TfWorkspace == "" {
		t.Fatalf("bad created state: %+v", created)
	}

	waitFor(t, 2*time.Second, func() bool {
		return store.getInstance(created.ID) != nil && store.getInstance(created.ID).Status == StatusRunning
	}, "instance running")
	got := store.getInstance(created.ID)
	if got.PublicIP != "1.2.3.4" || got.PrivateIP != "10.0.0.4" {
		t.Fatalf("ip not applied: %+v", got)
	}
	wantExpire := got.UpdatedAt.Add(3600 * time.Second)
	if !got.ExpiresAt.Equal(wantExpire) {
		t.Fatalf("expiresAt = %v want %v (apply 完成时刻 + duration)", got.ExpiresAt, wantExpire)
	}

	// 续用一次。
	rec = doJSON(t, srv, "POST", fmt.Sprintf("/api/v1/instances/%d/renew", created.ID), tok, map[string]any{"durationSec": 3600})
	if rec.Code != http.StatusOK {
		t.Fatalf("renew: %d %s", rec.Code, rec.Body.String())
	}
	renewed := store.getInstance(created.ID)
	if renewed.RenewedAt == nil {
		t.Fatal("renewedAt not set")
	}
	// 上限 7200：now+3600 不裁剪。
	if !renewed.ExpiresAt.Equal(renewed.RenewedAt.Add(3600 * time.Second)) {
		t.Fatalf("renewed expire = %v want renewedAt+3600", renewed.ExpiresAt)
	}

	// 第二次续用拒绝。
	rec = doJSON(t, srv, "POST", fmt.Sprintf("/api/v1/instances/%d/renew", created.ID), tok, map[string]any{"durationSec": 3600})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("second renew: want 400 got %d", rec.Code)
	}

	// 销毁：所有者，触发 tofu Destroy。
	rec = doJSON(t, srv, "DELETE", fmt.Sprintf("/api/v1/instances/%d", created.ID), tok, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	waitFor(t, 2*time.Second, func() bool {
		return store.getInstance(created.ID).Status == StatusDestroyed
	}, "instance destroyed")
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.destroys) != 1 || runner.destroys[0] != created.TfWorkspace {
		t.Fatalf("destroy calls = %v", runner.destroys)
	}
	if len(runner.applied) != 1 {
		t.Fatalf("apply calls = %v", runner.applied)
	}
}

func TestNonOwnerInstanceForbidden(t *testing.T) {
	store, _, h := newTestServer(t)
	owner := seedUser(t, store, h, "user", 0)
	inst := &Instance{ID: 55, UserID: 7, Status: StatusRunning, TfWorkspace: "ws", ExpiresAt: h.t().Add(time.Hour)}
	store.instances[55] = inst

	// 另一个用户。
	otherTok := mustIssue(t, store, h, 8, "user")
	srv := h.Routes()
	rec := doJSON(t, srv, "GET", "/api/v1/instances/55", otherTok, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-owner get: want 403 got %d", rec.Code)
	}
	rec = doJSON(t, srv, "DELETE", "/api/v1/instances/55", otherTok, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-owner delete: want 403 got %d", rec.Code)
	}
	// admin 放行。
	adminTok := mustIssue(t, store, h, 9, "admin")
	rec = doJSON(t, srv, "GET", "/api/v1/instances/55", adminTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin get: want 200 got %d", rec.Code)
	}
	_ = owner
}

func mustIssue(t *testing.T, store *fakeStore, h *Handler, id int64, role string) string {
	t.Helper()
	u := &auth.User{ID: id, Username: fmt.Sprintf("u%d", id), Role: auth.Role(role), Status: auth.StatusActive, Provider: "local"}
	store.mu.Lock()
	store.users[id] = u
	store.mu.Unlock()
	tok, err := h.Auth.IssueJWT(store.users[id])
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	return tok
}
