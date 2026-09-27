package api

import (
	"fmt"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/linuxsuren/open-cloud-web/internal/auth"
	"github.com/linuxsuren/open-cloud-web/internal/secrets"
)

func TestCloudAccountCRUD(t *testing.T) {
	store, _, h := newTestServer(t)
	srv := h.Routes()
	tok := seedUser(t, store, h, "user", 0)

	// 创建。
	rec := doJSON(t, srv, "POST", "/api/v1/cloud-accounts", tok, map[string]string{
		"name": "我的阿里云", "provider": "alicloud", "accessKey": "AKID123", "secretKey": "secret-123456",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var acct struct{ ID int64 `json:"id"` }
	if err := json.Unmarshal(rec.Body.Bytes(), &acct); err != nil {
		t.Fatal(err)
	}
	// 列表：secret 不得回显。
	rec = doJSON(t, srv, "GET", "/api/v1/cloud-accounts", tok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d", rec.Code)
	}
	if rec.Body.String() == "" || contains(rec.Body.String(), "secret-123456") {
		t.Fatal("secret 明文泄漏")
	}
	// 加密落库校验。
	raw, _ := store.GetCloudAccount(acct.ID)
	if raw.SecretEnc == "secret-123456" {
		t.Fatal("secret 未加密存储")
	}
	if _, err := secrets.Decrypt(raw.SecretEnc, "test-enc-secret"); err != nil {
		t.Fatalf("secret 无法解密: %v", err)
	}
	// 删除。
	path := "/api/v1/cloud-accounts/" + fmt.Sprint(acct.ID)
	rec = doJSON(t, srv, "DELETE", path, tok, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", rec.Code)
	}
}

func TestCloudAccountOwnership(t *testing.T) {
	store, _, h := newTestServer(t)
	srv := h.Routes()
	tokA := seedUser(t, store, h, "user", 0)

	rec := doJSON(t, srv, "POST", "/api/v1/cloud-accounts", tokA, map[string]string{
		"name": "a", "provider": "volcengine", "accessKey": "AK", "secretKey": "secret-123456",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d", rec.Code)
	}

	// 另一个用户（id 8）访问他人账号 → 403。
	store.mu.Lock()
	store.users[8] = &auth.User{ID: 8, Username: "u8", Role: auth.RoleUser, Status: auth.StatusActive, Provider: "local"}
	store.mu.Unlock()
	tokB, _ := h.Auth.IssueJWT(store.users[8])
	for _, target := range []string{
		"GET /api/v1/cloud-accounts/101/regions",
		"GET /api/v1/cloud-accounts/101/images?region=r",
		"DELETE /api/v1/cloud-accounts/101",
	} {
		method, path, _ := splitTarget(target)
		rec := doJSON(t, srv, method, path, tokB, nil)
		if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
			t.Fatalf("%s: want 403/404 got %d", target, rec.Code)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

func splitTarget(t string) (string, string, string) {
	for i := 0; i < len(t); i++ {
		if t[i] == ' ' {
			return t[:i], t[i+1:], ""
		}
	}
	return "GET", t, ""
}
