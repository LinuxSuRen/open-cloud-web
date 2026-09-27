package api

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestPATFlow(t *testing.T) {
	store, _, h := newTestServer(t)
	tok := seedUser(t, store, h, "user", 0)
	srv := h.Routes()

	// 创建 PAT。
	rec := doJSON(t, srv, "POST", "/api/v1/auth/pats", tok, map[string]any{"name": "cli", "expiresInDays": 30})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create pat: %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID    int64  `json:"id"`
		Token string `json:"token"`
	}
	decodeBody(t, rec, &created)
	if !strings.HasPrefix(created.Token, "pat_ocw_") {
		t.Fatalf("bad pat: %+v", created)
	}

	// PAT 可直接作为 Bearer 认证。
	rec = doJSON(t, srv, "GET", "/api/v1/me", created.Token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("pat auth: %d %s", rec.Code, rec.Body.String())
	}

	// PAT 换 JWT。
	rec = doJSON(t, srv, "POST", "/api/v1/auth/token", "", map[string]string{"token": created.Token})
	if rec.Code != http.StatusOK {
		t.Fatalf("exchange: %d %s", rec.Code, rec.Body.String())
	}
	var ex struct {
		Token string `json:"token"`
	}
	decodeBody(t, rec, &ex)
	rec = doJSON(t, srv, "GET", "/api/v1/me", ex.Token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("exchanged jwt auth: %d", rec.Code)
	}

	// 列表不泄露哈希/明文。
	rec = doJSON(t, srv, "GET", "/api/v1/auth/pats", tok, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"name":"cli"`) {
		t.Fatalf("list pats: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), created.Token) {
		t.Fatal("pat plaintext leaked in list")
	}

	// 删除他人的 PAT → 403。
	rec = doJSON(t, srv, "DELETE", "/api/v1/auth/pats/999999", tok, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("delete foreign pat: want 403 got %d", rec.Code)
	}

	// 删除自己的 → 该 PAT 立即失效。
	rec = doJSON(t, srv, "DELETE", "/api/v1/auth/pats/"+strconv.FormatInt(created.ID, 10), tok, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete pat: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, srv, "GET", "/api/v1/me", created.Token, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("deleted pat still valid: %d", rec.Code)
	}
}

func TestConcurrentCreatingLimit(t *testing.T) {
	store, runner, h := newTestServer(t)
	block := make(chan struct{})
	runner.mu.Lock()
	runner.applyBlock = block
	runner.mu.Unlock()
	tok := seedUser(t, store, h, "user", 0)
	srv := h.Routes()
	acctID := seedAccount(t, store)
	body := map[string]any{"cloudAccountID": acctID, "region": "r", "zone": "z", "imageID": "i", "instanceType": "s"}
	for i := 0; i < 5; i++ {
		rec := doJSON(t, srv, "POST", "/api/v1/instances", tok, body)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("create %d: %d %s", i, rec.Code, rec.Body.String())
		}
	}
	rec := doJSON(t, srv, "POST", "/api/v1/instances", tok, body)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("6th creating: want 429 got %d", rec.Code)
	}
	close(block)
}
